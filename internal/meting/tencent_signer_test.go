package meting

import "testing"

func TestTencentSigner(t *testing.T) {
	signer, err := newTencentSigner()
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}

	tests := []struct {
		name    string
		input   string
		expects string
	}{
		{"abc", "abc", "zzc7a68a42pouohx0jk5xfki7t91l4tsmcay4a9c12c73"},
		{"empty", "", "zzc9de725bt0s1xwro4fjxqxrzgmmisvau3ho23fbb1ee"},
		{"json", "{\"a\":1}", "zzc84058c2cvtr8srx4t93487bczdy4olvdm885d27e4"},
		{"search", "{\"req_1\":{\"module\":\"music.search.SearchCgiService\",\"method\":\"DoSearchForQQMusicDesktop\",\"param\":{\"remoteplace\":\"txt.mqq.all\",\"query\":\"周杰伦\",\"search_type\":0,\"num_per_page\":3,\"page_num\":1}}}", "zzcecb514cyjctuh8uponu7mstzqa13e7brek1e14c050"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := signer.Sign(test.input)
			if err != nil {
				t.Fatalf("sign: %v", err)
			}
			if got != test.expects {
				t.Fatalf("unexpected signature: got %q want %q", got, test.expects)
			}
		})
	}
}
