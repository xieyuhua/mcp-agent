// MCP 权限后台前端（原生 JS，无构建依赖，可随二进制内嵌或外部目录加载）。
const $ = (sel) => document.querySelector(sel);
const $$ = (sel) => Array.from(document.querySelectorAll(sel));

let TOKEN = localStorage.getItem("mcp_admin_token") || "";
let ROLES = []; // 缓存角色列表

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

function uploadFile(path, file) {
  const form = new FormData();
  form.append("file", file);
  return fetch(path, {
    method: "POST",
    headers: { "Authorization": "Bearer " + TOKEN },
    body: form,
  }).then(async (r) => {
    if (r.status === 401) {
      logout();
      throw new Error("登录已失效，请重新登录");
    }
    const data = await r.json().catch(() => ({}));
    if (!r.ok) throw new Error(data.error || ("HTTP " + r.status));
    return data;
  });
}

function download(path, filename) {
  fetch(path, { headers: { "Authorization": "Bearer " + TOKEN } })
    .then((r) => {
      if (r.status === 401) throw new Error("登录已失效");
      if (!r.ok) return r.json().then((d) => { throw new Error(d.error || ("HTTP " + r.status)); });
      return r.blob();
    })
    .then((blob) => {
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url; a.download = filename;
      document.body.appendChild(a); a.click(); a.remove();
      URL.revokeObjectURL(url);
    })
    .catch((e) => toast(e.message, false));
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

function showPanel(tab) {
  $$(".tab").forEach((x) => x.classList.toggle("active", x.dataset.tab === tab));
  ["roles", "policies", "fields", "masks"].forEach((id) => {
    $("#" + id).classList.toggle("hidden", id !== tab);
  });
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
    await loadRoles();
    loadPolicies();
  } catch (err) {
    $("#loginErr").textContent = err.message;
  }
});
$("#logout").addEventListener("click", logout);

// ---- Tab 切换 ----
$$(".tab").forEach((b) => b.addEventListener("click", () => {
  const tab = b.dataset.tab;
  showPanel(tab);
  if (tab === "roles") loadRoles();
  else if (tab === "policies") loadPolicies();
  else if (tab === "fields") loadFields();
  else loadMasks();
}));

// ---- 角色管理 ----
async function loadRoles() {
  try {
    const data = await api("/api/admin/roles");
    ROLES = data.roles || [];
    const tb = $("#roleTable tbody");
    tb.innerHTML = "";
    ROLES.forEach((r) => {
      const tr = document.createElement("tr");
      tr.innerHTML = `
        <td>${esc(r.tenant_id || "(平台默认)")}</td>
        <td>${esc(r.name)}</td>
        <td>${esc(r.display_name)}</td>
        <td>${esc(r.description || "")}</td>
        <td>${r.is_builtin ? "是" : "否"}</td>
        <td>${esc(r.updated_by || "")}</td>
        <td>${esc(r.updated_at || "")}</td>
        <td>${r.is_builtin ? "" : `<button class="link del" data-t="${esc(r.tenant_id)}" data-n="${esc(r.name)}">删除</button>`}</td>`;
      tb.appendChild(tr);
    });
    tb.querySelectorAll(".del").forEach((b) => b.addEventListener("click", async () => {
      if (!confirm(`确认删除角色 ${b.dataset.n}？`)) return;
      try {
        await api(`/api/admin/roles?tenant_id=${encodeURIComponent(b.dataset.t)}&name=${encodeURIComponent(b.dataset.n)}`, { method: "DELETE" });
        toast("已删除"); loadRoles();
      } catch (e) { toast(e.message, false); }
    }));
  } catch (e) { toast(e.message, false); }
}
$("#refreshRoles").addEventListener("click", loadRoles);
$("#newRole").addEventListener("click", () => openRoleModal());

function openRoleModal() {
  $("#modalTitle").textContent = "新增角色";
  $("#modalBody").innerHTML = `
    <label>租户ID（留空=平台全局）<input id="fr_tenant" placeholder="如 t1"></label>
    <label>角色标识<input id="fr_name" placeholder="如 custom_ops"></label>
    <label>显示名称<input id="fr_display" placeholder="如 运营专员"></label>
    <label>描述<textarea id="fr_desc" rows="3" placeholder="角色说明"></textarea></label>`;
  showModal(async () => {
    const body = {
      tenant_id: $("#fr_tenant").value.trim(),
      name: $("#fr_name").value.trim(),
      display_name: $("#fr_display").value.trim(),
      description: $("#fr_desc").value.trim(),
    };
    await api("/api/admin/roles", { method: "POST", body: JSON.stringify(body) });
    toast("已保存"); loadRoles();
  });
}

function roleOptions(selected) {
  return ROLES.map((r) => `<option value="${esc(r.name)}" ${r.name === selected ? "selected" : ""}>${esc(r.name)}${r.display_name ? " / " + esc(r.display_name) : ""}</option>`).join("");
}

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
        toast("已删除"); loadPolicies();
      } catch (e) { toast(e.message, false); }
    }));
  } catch (e) { toast(e.message, false); }
}
$("#refreshPolicies").addEventListener("click", loadPolicies);
$("#newPolicy").addEventListener("click", () => openPolicyModal());
$("#exportPolicies").addEventListener("click", () => download("/api/admin/policies/export", "policies.csv"));
$("#importPolicies").addEventListener("change", (e) => {
  const file = e.target.files[0];
  if (!file) return;
  uploadFile("/api/admin/policies/import", file).then((d) => { toast(`已导入 ${d.imported} 条`); loadPolicies(); }).catch((e) => toast(e.message, false));
  e.target.value = "";
});

function openPolicyModal() {
  $("#modalTitle").textContent = "新增角色策略";
  $("#modalBody").innerHTML = `
    <label>租户ID（留空=平台默认）<input id="f_tenant" placeholder="如 t1"></label>
    <label>角色<select id="f_role">${roleOptions("")}</select></label>
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
    toast("已保存"); loadPolicies();
  });
}

// ---- 字段权限 ----
async function loadFields() {
  try {
    const data = await api("/api/admin/field-permissions");
    const tb = $("#fieldTable tbody");
    tb.innerHTML = "";
    (data.field_permissions || []).forEach((r) => {
      const tr = document.createElement("tr");
      tr.innerHTML = `
        <td>${esc(r.tenant_id || "(平台默认)")}</td>
        <td>${esc(r.role)}</td>
        <td>${esc(r.table)}</td>
        <td>${esc(r.column)}</td>
        <td><span class="badge ${r.hidden ? 'hide' : 'show'}">${r.hidden ? "隐藏" : "可见"}</span></td>
        <td>${esc(r.updated_by || "")}</td>
        <td>${esc(r.updated_at || "")}</td>
        <td><button class="link del" data-t="${esc(r.tenant_id)}" data-r="${esc(r.role)}" data-tb="${esc(r.table)}" data-c="${esc(r.column)}">删除</button></td>`;
      tb.appendChild(tr);
    });
    tb.querySelectorAll(".del").forEach((b) => b.addEventListener("click", async () => {
      if (!confirm(`确认删除字段权限 ${b.dataset.tb}.${b.dataset.c} (${b.dataset.r})？`)) return;
      try {
        await api(`/api/admin/field-permissions?tenant_id=${encodeURIComponent(b.dataset.t)}&role=${encodeURIComponent(b.dataset.r)}&table=${encodeURIComponent(b.dataset.tb)}&column=${encodeURIComponent(b.dataset.c)}`, { method: "DELETE" });
        toast("已删除"); loadFields();
      } catch (e) { toast(e.message, false); }
    }));
  } catch (e) { toast(e.message, false); }
}
$("#refreshFields").addEventListener("click", loadFields);
$("#newField").addEventListener("click", () => openFieldModal());
$("#exportFields").addEventListener("click", () => download("/api/admin/field-permissions/export", "field_permissions.csv"));
$("#importFields").addEventListener("change", (e) => {
  const file = e.target.files[0];
  if (!file) return;
  uploadFile("/api/admin/field-permissions/import", file).then((d) => { toast(`已导入 ${d.imported} 条`); loadFields(); }).catch((e) => toast(e.message, false));
  e.target.value = "";
});

function openFieldModal() {
  $("#modalTitle").textContent = "新增字段权限";
  $("#modalBody").innerHTML = `
    <label>租户ID（留空=平台默认）<input id="fp_tenant" placeholder="如 t1"></label>
    <label>角色<select id="fp_role">${roleOptions("")}</select></label>
    <label>表名<input id="fp_table" placeholder="如 customers"></label>
    <label>列名<input id="fp_column" placeholder="如 id_card"></label>
    <label>状态<select id="fp_hidden">
      <option value="true">隐藏</option>
      <option value="false">可见</option>
    </select></label>`;
  showModal(async () => {
    const body = {
      tenant_id: $("#fp_tenant").value.trim(),
      role: $("#fp_role").value,
      table: $("#fp_table").value.trim(),
      column: $("#fp_column").value.trim(),
      hidden: $("#fp_hidden").value === "true",
    };
    await api("/api/admin/field-permissions", { method: "POST", body: JSON.stringify(body) });
    toast("已保存"); loadFields();
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
        toast("已删除"); loadMasks();
      } catch (e) { toast(e.message, false); }
    }));
  } catch (e) { toast(e.message, false); }
}
$("#refreshMasks").addEventListener("click", loadMasks);
$("#newMask").addEventListener("click", () => openMaskModal());
$("#exportMasks").addEventListener("click", () => download("/api/admin/mask-rules/export", "mask_rules.csv"));
$("#importMasks").addEventListener("change", (e) => {
  const file = e.target.files[0];
  if (!file) return;
  uploadFile("/api/admin/mask-rules/import", file).then((d) => { toast(`已导入 ${d.imported} 条`); loadMasks(); }).catch((e) => toast(e.message, false));
  e.target.value = "";
});

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
    toast("已保存"); loadMasks();
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
  loadRoles().then(() => loadPolicies());
}
