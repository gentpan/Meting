const providerOrigin = location.origin;

const providerLabelMap = {
  netease: "网易云音乐",
  tencent: "QQ 音乐",
  kugou: "酷狗音乐",
};

const statusLabelMap = {
  live: "在线",
  partial: "部分可用",
  cookie_required: "需要 Cookie",
};

const resourceLabelMap = {
  search: "搜索",
  songs: "歌曲",
  playlists: "歌单",
  albums: "专辑",
  artists: "歌手",
  stream: "直链",
  cover: "封面",
  lyric: "歌词",
};

const noteLabelMap = {
  kugou: "搜索和元数据接口可用。歌曲直链通常依赖有效的酷狗浏览器或客户端 Cookie，当前会自动尝试旧链路回退；配置 KUGOU_COOKIE 后成功率会更高。",
  netease: "当前使用仍可返回数据的旧版网页 JSON 接口。搜索、元数据、歌词和封面可用；歌曲直链越来越依赖账号状态或地域可用性。",
  tencent: "歌曲、歌单、专辑、封面和歌词接口可用。搜索优先走当前签名桌面搜索链路，拿不到结果时自动回退 smartbox；歌曲直链仍受 vkey 和版权限制影响。",
};

function providerCard(provider) {
  const resources = provider.resources.map((item) => `<li>${resourceLabelMap[item] || item}</li>`).join("");
  const title = providerLabelMap[provider.name] || provider.display_name || provider.name;
  const status = statusLabelMap[provider.status] || provider.status;
  const notes = noteLabelMap[provider.name] || provider.notes;
  return `
    <article class="provider-card">
      <p class="card-label">平台状态</p>
      <h3>${title}</h3>
      <span class="status-pill ${provider.status}">${status}</span>
      <p>${notes}</p>
      <ul>${resources}</ul>
    </article>
  `;
}

function syncProbeLabels(form) {
  const resource = form.resource.value;
  const subject = document.querySelector("#probe-subject-label");
  const extra = document.querySelector("#probe-extra-label");

  if (resource === "search") {
    subject.textContent = "关键词";
    extra.textContent = "数量";
    return;
  }

  subject.textContent = "编号";
  extra.textContent = resource === "playlist" ? "未使用" : "附加参数";
}

function buildProbeURL(form) {
  const provider = form.provider.value;
  const resource = form.resource.value;
  const value = form.value.value.trim();
  const extra = form.extra.value.trim();

  switch (resource) {
    case "search":
      return `${providerOrigin}/api/v1/${provider}/search?q=${encodeURIComponent(value)}&page=1&limit=${encodeURIComponent(extra || "5")}`;
    case "song":
      return `${providerOrigin}/api/v1/${provider}/songs/${encodeURIComponent(value)}`;
    case "playlist":
      return `${providerOrigin}/api/v1/${provider}/playlists/${encodeURIComponent(value)}`;
    case "stream":
      return `${providerOrigin}/api/v1/${provider}/songs/${encodeURIComponent(value)}/stream?redirect=false`;
    default:
      return `${providerOrigin}/api/v1/providers`;
  }
}

async function renderProviders() {
  const container = document.querySelector("#provider-center-list");
  const response = await fetch(`${providerOrigin}/api/v1/providers`);
  const payload = await response.json();
  container.innerHTML = payload.providers.map(providerCard).join("");
}

async function bootProbe() {
  const form = document.querySelector("#provider-probe-form");
  if (!form) {
    return;
  }

  const probeURL = document.querySelector("#probe-url");
  const probeOutput = document.querySelector("#probe-output");

  form.resource.addEventListener("change", () => syncProbeLabels(form));
  syncProbeLabels(form);

  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    const url = buildProbeURL(form);
    probeURL.textContent = url;
    probeOutput.textContent = "加载中...";
    try {
      const response = await fetch(url);
      const text = await response.text();
      try {
        probeOutput.textContent = JSON.stringify(JSON.parse(text), null, 2);
      } catch {
        probeOutput.textContent = text;
      }
    } catch (error) {
      probeOutput.textContent = String(error);
    }
  });
}

document.querySelectorAll("[data-origin-label]").forEach((node) => {
  node.textContent = providerOrigin;
});

renderProviders().catch((error) => {
  const container = document.querySelector("#provider-center-list");
  container.innerHTML = `<article class="provider-card"><p class="card-label">错误</p><h3>平台加载失败</h3><p>${String(error)}</p></article>`;
});
bootProbe();
