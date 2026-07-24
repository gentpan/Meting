package site

import (
	"encoding/json"
	"net/http"
	"strings"

	"metingio/internal/auth"
	"metingio/internal/meting"
)

// handleAdmin dispatches everything under /api/v1/admin/. The whole subtree is
// gated by the special operator token (the same one that protects /stats), so
// only the operator can read account status or start a scan-login.
//
// Routes:
//
//	GET    /api/v1/admin/credentials            list account slots + health
//	POST   /api/v1/admin/credentials/{provider} set a cookie manually {"cookie":"..."}
//	DELETE /api/v1/admin/credentials/{provider} clear a stored cookie
//	GET    /api/v1/admin/sources                list scannable login sources
//	POST   /api/v1/admin/qr/{source}            start a scan session -> {key,url,image_url}
//	GET    /api/v1/admin/qr/{source}?key=...    poll; on success persists cookie + probes health
//	POST   /api/v1/admin/check/{provider}       re-probe a stored cookie's health
func handleAdmin(store *auth.Store, w http.ResponseWriter, r *http.Request) {
	token := auth.ExtractToken(r)
	if token == "" || !store.IsSpecial(token) {
		writeError(w, http.StatusForbidden, "admin endpoints require the special operator token")
		return
	}

	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/admin/"), "/")
	parts := strings.Split(rest, "/")
	head := parts[0]

	switch head {
	case "credentials":
		if len(parts) == 1 {
			adminListCredentials(store, w, r)
			return
		}
		adminCredentialByProvider(store, w, r, parts[1])
	case "sources":
		writeJSON(w, http.StatusOK, map[string]any{"sources": meting.QRLoginSources()})
	case "qr":
		if len(parts) < 2 {
			writeError(w, http.StatusNotFound, "missing qr source")
			return
		}
		adminQR(store, w, r, parts[1])
	case "check":
		if len(parts) < 2 {
			writeError(w, http.StatusNotFound, "missing provider")
			return
		}
		adminCheck(store, w, r, parts[1])
	default:
		writeError(w, http.StatusNotFound, "unknown admin route")
	}
}

func adminListCredentials(store *auth.Store, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	creds, err := store.ListCredentials(meting.CredentialProviders())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list credentials failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"credentials": creds,
		"sources":     meting.QRLoginSources(),
	})
}

func adminCredentialByProvider(store *auth.Store, w http.ResponseWriter, r *http.Request, provider string) {
	if !knownProvider(provider) {
		writeError(w, http.StatusNotFound, "unknown provider")
		return
	}
	switch r.Method {
	case http.MethodPost:
		var body struct {
			Cookie string `json:"cookie"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		cookie := meting.NormalizeCookieInput(body.Cookie)
		if cookie == "" {
			writeError(w, http.StatusBadRequest, "cookie is required")
			return
		}
		if err := store.SetCredential(provider, cookie); err != nil {
			writeError(w, http.StatusInternalServerError, "save cookie failed")
			return
		}
		persistHealth(store, provider, cookie)
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "provider": provider})
	case http.MethodDelete:
		if err := store.DeleteCredential(provider); err != nil {
			writeError(w, http.StatusInternalServerError, "delete cookie failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "deleted", "provider": provider})
	default:
		writeError(w, http.StatusMethodNotAllowed, "use POST or DELETE")
	}
}

func adminQR(store *auth.Store, w http.ResponseWriter, r *http.Request, source string) {
	provider, ok := meting.CredentialKeyForSource(source)
	if !ok {
		writeError(w, http.StatusNotFound, "unsupported qr login source")
		return
	}
	switch r.Method {
	case http.MethodPost:
		session, err := meting.CreateQR(source)
		if err != nil {
			writeError(w, http.StatusBadGateway, "create qr failed: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, session)
	case http.MethodGet:
		key := r.URL.Query().Get("key")
		if strings.TrimSpace(key) == "" {
			writeError(w, http.StatusBadRequest, "missing qr key")
			return
		}
		result, err := meting.CheckQR(source, key)
		if err != nil {
			writeError(w, http.StatusBadGateway, "check qr failed: "+err.Error())
			return
		}
		resp := map[string]any{
			"source":   source,
			"provider": provider,
			"status":   string(result.Status),
			"message":  result.Message,
		}
		// On success, persist the cookie under the provider key and probe health.
		if string(result.Status) == "success" && strings.TrimSpace(result.Cookie) != "" {
			if err := store.SetCredential(provider, result.Cookie); err != nil {
				writeError(w, http.StatusInternalServerError, "save cookie failed")
				return
			}
			resp["health"] = persistHealth(store, provider, result.Cookie)
		}
		writeJSON(w, http.StatusOK, resp)
	default:
		writeError(w, http.StatusMethodNotAllowed, "use POST to create, GET to poll")
	}
}

func adminCheck(store *auth.Store, w http.ResponseWriter, r *http.Request, provider string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	if !knownProvider(provider) {
		writeError(w, http.StatusNotFound, "unknown provider")
		return
	}
	cookie := store.Cookie(provider)
	if strings.TrimSpace(cookie) == "" {
		writeError(w, http.StatusBadRequest, "no cookie stored for "+provider)
		return
	}
	writeJSON(w, http.StatusOK, persistHealth(store, provider, cookie))
}

// persistHealth probes the cookie and writes the result back to the store,
// returning the probe outcome for the response.
func persistHealth(store *auth.Store, provider, cookie string) meting.HealthResult {
	res := meting.CheckHealth(provider, cookie)
	_ = store.UpdateHealth(provider, res.Status, res.Detail, res.VIPExpiry)
	return res
}

func knownProvider(provider string) bool {
	for _, p := range meting.CredentialProviders() {
		if p == provider {
			return true
		}
	}
	return false
}
