const origin = location.origin;

function $(selector) {
  return document.querySelector(selector);
}

function updateOriginLabels() {
  document.querySelectorAll("[data-origin-label]").forEach((node) => {
    node.textContent = origin;
  });
  document.querySelectorAll("[data-origin-template]").forEach((node) => {
    node.textContent = node.textContent.replace("{{ORIGIN}}", origin);
  });
}

async function loadProviders() {
  try {
    const payload = await fetch(`${origin}/api/v1/providers`).then((res) => res.json());
    document.querySelectorAll("[data-provider-count]").forEach((node) => {
      node.textContent = String(payload.providers.length);
    });
  } catch (error) {
    console.error(error);
  }
}

function buildExplorerURL(form) {
  const provider = form.provider.value;
  const resource = form.resource.value;
  const value = form.value.value.trim();
  const extra = form.extra.value.trim();

  switch (resource) {
    case "search":
      return `${origin}/api/v1/${provider}/search?q=${encodeURIComponent(value)}&page=1&limit=${encodeURIComponent(extra || "5")}`;
    case "songs":
      return `${origin}/api/v1/${provider}/songs/${encodeURIComponent(value)}`;
    case "stream":
      return `${origin}/api/v1/${provider}/songs/${encodeURIComponent(value)}/stream?redirect=false`;
    case "cover":
      return `${origin}/api/v1/${provider}/songs/${encodeURIComponent(value)}/cover?redirect=false&size=${encodeURIComponent(extra || "600")}`;
    case "lyric":
      return `${origin}/api/v1/${provider}/songs/${encodeURIComponent(value)}/lyric?format=json`;
    case "playlists":
      return `${origin}/api/v1/${provider}/playlists/${encodeURIComponent(value)}`;
    case "albums":
      return `${origin}/api/v1/${provider}/albums/${encodeURIComponent(value)}`;
    case "artists":
      return `${origin}/api/v1/${provider}/artists/${encodeURIComponent(value)}?limit=${encodeURIComponent(extra || "20")}`;
    default:
      return `${origin}/api/v1/providers`;
  }
}

function syncFormLabels(form, subjectSelector, extraSelector) {
  const resource = form.resource.value;
  const subject = $(subjectSelector);
  const extra = $(extraSelector);

  if (resource === "search") {
    subject.textContent = "关键词";
    extra.textContent = "数量";
    form.value.value = form.value.value || "周杰伦";
    form.extra.value = form.extra.value || "5";
    return;
  }

  subject.textContent = "编号";
  if (resource === "cover") {
    extra.textContent = "尺寸";
    form.extra.value = form.extra.value || "600";
    return;
  }

  if (resource === "artists") {
    extra.textContent = "数量";
    form.extra.value = form.extra.value || "20";
    return;
  }

  extra.textContent = "附加参数";
}

async function bootExplorer() {
  const form = $("#explorer-form");
  if (!form) {
    return;
  }

  const requestURL = $("#request-url");
  const output = $("#response-output");

  form.resource.addEventListener("change", () => syncFormLabels(form, "#subject-label", "#extra-label"));
  syncFormLabels(form, "#subject-label", "#extra-label");

  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    const url = buildExplorerURL(form);
    requestURL.textContent = url;
    output.textContent = "加载中...";
    try {
      const response = await fetch(url);
      const text = await response.text();
      try {
        output.textContent = JSON.stringify(JSON.parse(text), null, 2);
      } catch {
        output.textContent = text;
      }
    } catch (error) {
      output.textContent = String(error);
    }
  });
}

updateOriginLabels();
loadProviders();
bootExplorer();
