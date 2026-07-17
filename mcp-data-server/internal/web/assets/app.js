// MCP 权限后台前端（原生 JS，无构建依赖，可随二进制内嵌或外部目录加载）。
const $ = (sel) => document.querySelector(sel);
const $$ = (sel) => Array.from(document.querySelectorAll(sel));

let TOKEN = localStorage.getItem("mcp_admin_token") || "";

function api(path, opts = {}) {
  opts.headers = Object.assign({}, opts.headers, { "Content-Type": "application/json" });
  if (TOKEN) opts.headers["Authorization"] = "Bearer " + TOKEN;
  return fetch(path, opts).then(async (r) => {
    if (r.status === 401) {
      logout();
      throw new Error("登录已失效，请重新登录");
    }
    const data = await r.json().catch(() => ({}));
    if (!r.ok) throw new Error(data.error || ("HTTP " + r.status));
    return data;
  });
}

function toast(msg, ok = true) {
  const t = $("#toast");
  t.textContent = msg;
  t.className = "toast" + (ok ? " ok" : " err");
  setTimeout(() => t.classList.add("hidden"), 2500);
}

function logout() {
  TOKEN = "";
  localStorage.removeItem("mcp_admin_token");
  $("#app").classList.add("hidden");
  $("#login").classList.remove("hidden");
}

// ---- 登录 ----
$("#loginForm").addEventListener("submit", async (e) => {
  e.preventDefault();
  $("#loginErr").textContent = "";
  try {
    const data = await api("/api/admin/login", {
      method: "POST",
      body: JSON.stringify({ username: $("#username").value, password: $("#password").value }),
    });
    TOKEN = data.token;
    localStorage.setItem("mcp_admin_token", TOKEN);
    $("#login").classList.add("hidden");
    $("#app").classList.remove("hidden");
    loadPolicies();
  } catch (err) {
    $("#loginErr").textContent = err.message;
  }
});
$("#logout").addEventListener("click", logout);

// ---- Tab 切换 ----
$$(".tab").forEach((b) => b.addEventListener("click", () => {
  $$(".tab").forEach((x) => x.classList.remove("active"));
  b.classList.add("active");
  const tab = b.dataset.tab;
  $("#policies").classList.toggle("hidden", tab !== "policies");
  $("#masks").classList.toggle("hidden", tab !== "masks");
  if (tab === "policies") loadPolicies();
  else loadMasks();
}));

// ---- 角色策略 ----
async function loadPolicies() {
  try {
    const data = await api("/api/admin/policies");
    const tb = $("#policyTable tbody");
    tb.innerHTML = "";
    data.policies.forEach((p) => {
      const tr = document.createElement("tr");
      tr.innerHTML = `
        <td>${esc(p.tenant_id || "(平台默认)")}</td>
        <td>${esc(p.role)}</td>
        <td>${esc(p.data_scope)}</td>
        <td>${esc((p.allowed_tables || []).join(", "))}</td>
        <td>${p.can_raw_sql ? "✅" : "❌"}</td>
        <td>${esc(p.updated_by || "")}</td>
        <td>${esc(p.updated_at || "")}</td>
        <td><button class="link del" data-t="${esc(p.tenant_id)}" data-r="${esc(p.role)}">删除</button></td>`;
      tb.appendChild(tr);
    });
    tb.querySelectorAll(".del").forEach((b) => b.addEventListener("click", async () => {
      if (!confirm(`确认删除策略 ${b.dataset.r} (${b.dataset.t || "平台默认"})？`)) return;
      try {
        await api(`/api/admin/policies?tenant_id=${encodeURIComponent(b.dataset.t)}&role=${encodeURIComponent(b.dataset.r)}`, { method: "DELETE" });
        toast("已删除");
        loadPolicies();
      } catch (e) { toast(e.message, false); }
    }));
  } catch (e) { toast(e.message, false); }
}

$("#refreshPolicies").addEventListener("click", loadPolicies);
$("#newPolicy").addEventListener("click", () => openPolicyModal(null));

function openPolicyModal() {
  $("#modalTitle").textContent = "新增角色策略";
  $("#modalBody").innerHTML = `
    <label>租户ID（留空=平台默认）<input id="f_tenant" placeholder="如 t1"></label>
    <label>角色<select id="f_role">
      <option>super_admin</option><option>region_manager</option>
      <option>store_manager</option><option>staff</option><option>analyst</option>
    </select></label>
    <label>数据范围<select id="f_scope">
      <option>all</option><option>tenant</option><option>region</option><option>store</option>
    </select></label>
    <label>可访问表（逗号分隔，如 customers,orders）<input id="f_tables" placeholder="留空=角色默认"></label>
    <label class="chk">允许原生SQL<input id="f_raw" type="checkbox"></label>`;
  showModal(async () => {
    const body = {
      tenant_id: $("#f_tenant").value.trim(),
      role: $("#f_role").value,
      data_scope: $("#f_scope").value,
      allowed_tables: $("#f_tables").value.split(",").map((s) => s.trim()).filter(Boolean),
      can_raw_sql: $("#f_raw").checked,
    };
    await api("/api/admin/policies", { method: "POST", body: JSON.stringify(body) });
    toast("已保存");
    loadPolicies();
  });
}

// ---- 脱敏规则 ----
async function loadMasks() {
  try {
    const data = await api("/api/admin/mask-rules");
    const tb = $("#maskTable tbody");
    tb.innerHTML = "";
    data.rules.forEach((r) => {
      const tr = document.createElement("tr");
      tr.innerHTML = `
        <td>${esc(r.tenant_id || "(平台默认)")}</td>
        <td>${esc(r.table)}</td>
        <td>${esc(r.column)}</td>
        <td>${esc(r.mask_type)}</td>
        <td>${r.enabled ? "✅" : "❌"}</td>
        <td>${esc(r.updated_by || "")}</td>
        <td>${esc(r.updated_at || "")}</td>
        <td><button class="link del" data-t="${esc(r.tenant_id)}" data-tb="${esc(r.table)}" data-c="${esc(r.column)}">删除</button></td>`;
      tb.appendChild(tr);
    });
    tb.querySelectorAll(".del").forEach((b) => b.addEventListener("click", async () => {
      if (!confirm(`确认删除脱敏规则 ${b.dataset.tb}.${b.dataset.c}？`)) return;
      try {
        await api(`/api/admin/mask-rules?tenant_id=${encodeURIComponent(b.dataset.t)}&table=${encodeURIComponent(b.dataset.tb)}&column=${encodeURIComponent(b.dataset.c)}`, { method: "DELETE" });
        toast("已删除");
        loadMasks();
      } catch (e) { toast(e.message, false); }
    }));
  } catch (e) { toast(e.message, false); }
}

$("#refreshMasks").addEventListener("click", loadMasks);
$("#newMask").addEventListener("click", () => openMaskModal());

function openMaskModal() {
  $("#modalTitle").textContent = "新增脱敏规则";
  $("#modalBody").innerHTML = `
    <label>租户ID（留空=平台默认）<input id="m_tenant" placeholder="如 t1"></label>
    <label>表名<input id="m_table" placeholder="如 customers"></label>
    <label>列名<input id="m_column" placeholder="如 phone"></label>
    <label>脱敏类型<select id="m_type">
      <option>phone</option><option>email</option><option>idcard</option>
      <option>name</option><option>money</option><option>secret</option>
    </select></label>
    <label class="chk">启用<input id="m_enabled" type="checkbox" checked></label>`;
  showModal(async () => {
    const body = {
      tenant_id: $("#m_tenant").value.trim(),
      table: $("#m_table").value.trim(),
      column: $("#m_column").value.trim(),
      mask_type: $("#m_type").value,
      enabled: $("#m_enabled").checked,
    };
    await api("/api/admin/mask-rules", { method: "POST", body: JSON.stringify(body) });
    toast("已保存");
    loadMasks();
  });
}

// ---- 弹窗 ----
function showModal(onSave) {
  $("#modal").classList.remove("hidden");
  $("#modalSave").onclick = async () => {
    try { await onSave(); $("#modal").classList.add("hidden"); }
    catch (e) { toast(e.message, false); }
  };
}
$("#modalCancel").addEventListener("click", () => $("#modal").classList.add("hidden"));

function esc(s) {
  return String(s == null ? "" : s).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}

// 启动：已登录则直接进
if (TOKEN) {
  $("#login").classList.add("hidden");
  $("#app").classList.remove("hidden");
  loadPolicies();
}
