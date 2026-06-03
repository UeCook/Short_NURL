/* ═══════════════════════════════════════════════════════════
   URL 短链服务 — 应用逻辑
   基于 Basecoat UI 设计系统
   所有 API 请求通过 ./api 端点，遵循 API 文档规范
   密钥不持久化 — 每次手动输入
   ═══════════════════════════════════════════════════════════ */

(function () {
  "use strict";

  /* ── 配置 ──────────────────────────────────────────────── */
  var API_BASE = "./api";
  var isPermanent = false; // 切换组状态：临时（默认） / 永久
  var manageListLoaded = false; // 追踪管理列表是否已加载

  /* ── DOM 辅助函数 ──────────────────────────────────────── */
  function $(sel) { return document.querySelector(sel); }

  /* ── 主题管理 ──────────────────────────────────────────── */
  function initTheme() {
    var stored = localStorage.getItem("su_theme");
    // 默认深色模式，仅当明确保存为 "light" 时切换为浅色
    if (stored === "light") {
      document.documentElement.classList.remove("dark");
    } else {
      document.documentElement.classList.add("dark");
    }
  }

  function toggleTheme() {
    var isDark = document.documentElement.classList.toggle("dark");
    localStorage.setItem("su_theme", isDark ? "dark" : "light");
  }

  /* ── Toast 提示通知 ────────────────────────────────────── */
  function showToast(message, type) {
    type = type || "success";
    var container = $("#toaster");
    if (!container) return;

    var iconSvg = type === "success"
      ? '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6 9 17l-5-5"/></svg>'
      : '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>';

    var toast = document.createElement("div");
    toast.className = "app-toast app-toast-" + type;
    toast.innerHTML =
      '<span class="app-toast-icon">' + iconSvg + '</span>' +
      '<span class="app-toast-msg">' + escapeHtml(message) + '</span>';

    container.appendChild(toast);
    requestAnimationFrame(function () { toast.classList.add("visible"); });

    setTimeout(function () {
      toast.classList.remove("visible");
      setTimeout(function () {
        if (toast.parentNode) toast.parentNode.removeChild(toast);
      }, 220);
    }, 10000);
  }

  /* ── 确认对话框 ────────────────────────────────────────── */
  function showConfirm(title, message) {
    return new Promise(function (resolve) {
      var overlay = document.createElement("div");
      overlay.className = "confirm-overlay";
      overlay.innerHTML =
        '<div class="confirm-box">' +
        '<div class="confirm-title">' + escapeHtml(title) + '</div>' +
        '<div class="confirm-msg">' + escapeHtml(message) + '</div>' +
        '<div class="confirm-actions">' +
        '<button class="btn btn-outline btn-sm" data-action="cancel">取消</button>' +
        '<button class="btn btn-destructive btn-sm" data-action="confirm">确认删除</button>' +
        '</div></div>';

      document.body.appendChild(overlay);
      requestAnimationFrame(function () { overlay.classList.add("visible"); });

      overlay.addEventListener("click", function (e) {
        var btn = e.target.closest("button");
        if (!btn) return;
        var action = btn.getAttribute("data-action");
        overlay.classList.remove("visible");
        setTimeout(function () {
          if (overlay.parentNode) overlay.parentNode.removeChild(overlay);
        }, 220);
        resolve(action === "confirm");
      });
    });
  }

  /* ── API 层（遵循 API 文档） ─────────────────────────────
     POST: 创建 / 删除 → 密钥在 JSON 请求体中
     GET:  列表 / 统计 → 密钥在 X-Token 请求头中
     所有请求通过 API_BASE (./api) 发送，禁止直接访问
  */
  function apiPost(endpoint, body) {
    return fetch(API_BASE + endpoint, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body)
    }).then(function (res) {
      return res.text().then(function (text) {
        var data;
        try { data = JSON.parse(text); } catch (e) {
          throw { status: res.status, message: "服务器响应格式错误" };
        }
        if (!res.ok) throw { status: res.status, message: data.error || "请求失败" };
        return data;
      });
    });
  }

  function apiGet(endpoint, key) {
    if (!key) return Promise.reject({ message: "请输入 API Key" });
    return fetch(API_BASE + endpoint, {
      headers: { "X-Token": key }
    }).then(function (res) {
      return res.text().then(function (text) {
        var data;
        try { data = JSON.parse(text); } catch (e) {
          throw { status: res.status, message: "服务器响应格式错误" };
        }
        if (!res.ok) throw { status: res.status, message: data.error || "请求失败" };
        return data;
      });
    });
  }

  /* ── 从输入框获取密钥 ─────────────────────────────────── */
  function getCreateKey() {
    return ($("#createKeyInput").value || "").trim();
  }

  function getManageKey() {
    return ($("#manageKeyInput").value || "").trim();
  }

  /* ── 切换按钮组逻辑（永久 / 临时） ─────────────────────── */
  function initToggleGroup() {
    var group = $("#ttlToggleGroup");
    if (!group) return;
    var btns = group.querySelectorAll(".toggle-btn");
    var controls = $("#ttlControls");

    setDefaultDatetime();

    btns.forEach(function (btn) {
      btn.addEventListener("click", function () {
        // 更新激活状态
        btns.forEach(function (b) { b.classList.remove("active"); });
        btn.classList.add("active");

        var val = btn.getAttribute("data-value");
        isPermanent = (val === "perm");

        // 滑动切换指示器 & 隐藏/显示有效期控件
        group.classList.toggle("is-perm", isPermanent);
        if (controls) {
          controls.style.display = isPermanent ? "none" : "";
        }
      });
    });
  }

  /* ── 有效期控件 ────────────────────────────────────────── */
  function initTTLControls() {
    var preset = $("#ttlPreset");
    var customBox = $("#ttlCustom");

    if (!preset) return;

    preset.addEventListener("change", function () {
      if (preset.value === "custom") {
        customBox.style.display = "";
        setDefaultDatetime();
      } else {
        customBox.style.display = "none";
      }
    });
  }

  function setDefaultDatetime() {
    var dt = $("#ttlDatetime");
    if (!dt) return;
    var future = new Date(Date.now() + 7 * 86400000);
    dt.value = toLocalISOString(future);
    dt.min = toLocalISOString(new Date());
  }

  function toLocalISOString(d) {
    var pad = function (n) { return n < 10 ? "0" + n : "" + n; };
    return d.getFullYear() + "-" + pad(d.getMonth() + 1) + "-" + pad(d.getDate()) +
      "T" + pad(d.getHours()) + ":" + pad(d.getMinutes());
  }

  function getTTL() {
    if (isPermanent) return 0;

    var presetVal = $("#ttlPreset").value;
    if (presetVal === "custom") {
      var dtVal = $("#ttlDatetime").value;
      if (!dtVal) return 604800;
      var target = new Date(dtVal).getTime();
      var now = Date.now();
      var diff = Math.floor((target - now) / 1000);
      return diff > 0 ? diff : 0;
    }
    return parseInt(presetVal, 10) || 604800;
  }

  /* ── 统计数据显示 ─────────────────────────────────────── */
  /* 权威来源：冷存储（JSON 文件，countActive 无状态遍历永不漂移）
   * 内部参考：热存储（Lua shared dict，仅供诊断对比）
   * 显示规则：
   *   - 主显示冷存储计数（与无头模式 CLI 数据源一致）
   *   - 热存储可用且有差异时 → 附带热存参考值供诊断
   *   - 热存储不可用 → 附带 ⚠ 标记提示
   */
  function loadStat(key) {
    if (!key) { renderStat(null); return; }
    apiGet("/stat", key)
      .then(function (data) { renderStat(data); })
      .catch(function (err) {
        showToast(err.message || "获取配额失败", "error");
        renderStat(null);
      });
  }

  function renderStat(data) {
    var permEl = $("#statPermValue");
    var tempEl = $("#statTempValue");
    if (!permEl || !tempEl) return;
    if (!data) {
      permEl.textContent = "\u2014";
      tempEl.textContent = "\u2014";
      return;
    }

    var hotOk = data.hot_available !== false;  // 热存储是否可用
    var permCount = data.perm_count;            // 冷存储（权威来源）
    var tempCount = data.temp_count;            // 冷存储（权威来源）
    var permLimit = data.perm_limit;
    var tempLimit = data.temp_limit;

    // 冷存储为主显示（无状态遍历，永不漂移）
    var permText = permCount + " / " + permLimit;
    var tempText = tempCount + " / " + tempLimit;

    // 热存漂移仅服务端日志记录（stat.php error_log），不暴露给用户
    // 热存储不可用时附带警告标记
    if (!hotOk) {
      permText += "  \u26A0 仅冷存";
      tempText += "  \u26A0 仅冷存";
    }

    permEl.textContent = permText;
    tempEl.textContent = tempText;
  }

  /* ── 创建短链 ──────────────────────────────────────────── */
  function handleCreate(e) {
    e.preventDefault();

    var url = ($("#urlInput").value || "").trim();
    var code = ($("#codeInput").value || "").trim();
    var ttl = getTTL();
    var key = getCreateKey();

    if (!url) { showToast("请输入目标链接", "error"); return; }
    if (!/^https?:\/\//i.test(url)) { showToast("链接必须以 http:// 或 https:// 开头", "error"); return; }
    if (!key) { showToast("请输入 API Key", "error"); return; }

    var body = { url: url, ttl: ttl, key: key };
    if (code) body.code = code;

    var btn = $("#createBtn");
    if (!btn) return;
    btn.disabled = true;
    btn.textContent = "创建中\u2026";

    apiPost("/create", body)
      .then(function (data) {
        if (data.synced === false) {
          // 热存储同步失败 — 短链已保存但暂不可用
          showToast(data.error || "热存储同步失败，短链暂不可用", "error");
          showSuccessView(data.short_url, data.exp || null);
        } else {
          showToast("短链创建成功");
          showSuccessView(data.short_url, data.exp || null);
        }
        $("#urlInput").value = "";
        $("#codeInput").value = "";
      })
      .catch(function (err) { showToast(err.message || "创建失败", "error"); })
      .finally(function () {
        btn.disabled = false;
        btn.textContent = "创建短链";
      });
  }

  /* ── 创建成功视图（创建后替换表单） ────────────────────── */
  function showSuccessView(shortUrl, exp) {
    // 隐藏表单，显示成功视图
    $("#createForm").classList.add("hidden");
    $("#resultBox").classList.remove("visible");

    var sv = $("#successView");
    $("#successUrl").textContent = shortUrl;
    var expEl = $("#successExp");
    if (exp && exp !== "permanent") {
      expEl.textContent = "过期: " + formatExp(exp);
      expEl.style.display = "";
    } else {
      expEl.textContent = "";
      expEl.style.display = "none";
    }
    sv.classList.add("visible");
  }

  function hideSuccessView() {
    // 显示表单，隐藏成功视图
    $("#createForm").classList.remove("hidden");
    $("#successView").classList.remove("visible");
    // 聚焦链接输入框以便快速重新输入
    var urlInput = $("#urlInput");
    if (urlInput) urlInput.focus();
  }

  function copySuccessLink() {
    var url = ($("#successUrl").textContent || "").trim();
    if (!url) return;
    navigator.clipboard.writeText(url)
      .then(function () { showToast("已复制到剪贴板"); })
      .catch(function () { showToast("复制失败", "error"); });
  }

  /* ── 短链列表 ──────────────────────────────────────────── */
  function loadList(key) {
    if (!key) { renderList(null, null); return; }
    apiGet("/list", key)
      .then(function (data) {
        renderList(data.permanent || [], data.temporary || []);
        manageListLoaded = true;
      })
      .catch(function (err) {
        showToast(err.message || "获取列表失败", "error");
        renderList(null, null);
      });
  }

  function renderList(permList, tempList) {
    var permEl = $("#permList");
    var tempEl = $("#tempList");

    if (!permEl || !tempEl) return;

    if (!permList && !tempList) {
      permEl.innerHTML = '<div class="url-list-empty">请输入 API Key 后查看列表</div>';
      tempEl.innerHTML = '<div class="url-list-empty">请输入 API Key 后查看列表</div>';
      return;
    }

    var permCount = permList.length;
    var tempCount = tempList.length;

    permEl.innerHTML = permCount > 0
      ? permList.map(function (item) { return renderUrlItem(item, "permanent"); }).join("")
      : '<div class="url-list-empty">暂无永久短链</div>';

    tempEl.innerHTML = tempCount > 0
      ? tempList.map(function (item) { return renderUrlItem(item, "temporary"); }).join("")
      : '<div class="url-list-empty">暂无临时短链</div>';
  }

  function renderUrlItem(item, type) {
    var badgeClass = type === "permanent" ? "badge-primary" : "badge-secondary";
    var badgeText = type === "permanent" ? "永久" : "临时";
    var expText = "";
    if (type === "temporary" && item.exp && item.exp !== "permanent") {
      expText = '<span class="url-item-exp">过期: ' + formatExp(item.exp) + '</span>';
    }

    return '<div class="url-item">' +
      '<span class="badge ' + badgeClass + '">' + badgeText + '</span>' +
      '<span class="url-item-code">' + escapeHtml(item.id) + '</span>' +
      '<span class="url-item-original" title="' + escapeAttr(item.url) + '">' + escapeHtml(item.url) + '</span>' +
      expText +
      '<div class="url-item-actions">' +
      '<button class="btn btn-ghost btn-sm-icon" data-copy="' + escapeAttr(item.lurl) + '" title="复制链接">' +
      '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect width="14" height="14" x="8" y="8" rx="2" ry="2"/><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"/></svg></button>' +
      '<button class="btn btn-ghost btn-sm-icon" data-delete="' + escapeAttr(item.id) + '" title="删除">' +
      '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 6h18"/><path d="M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6"/><path d="M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2"/></svg></button>' +
      '</div></div>';
  }

  /* ── 删除短链 ──────────────────────────────────────────── */
  function handleDelete(code) {
    var key = getManageKey();
    if (!key) { showToast("请输入 API Key", "error"); return; }
    showConfirm("删除短链", '确定要删除短链 "' + code + '" 吗？此操作不可撤销。')
      .then(function (confirmed) {
        if (!confirmed) return;
        apiPost("/delete", { code: code, key: key })
          .then(function () {
            showToast("短链 " + code + " 已删除");
            refreshManageData();
          })
          .catch(function (err) { showToast(err.message || "删除失败", "error"); });
      });
  }

  function handleCopy(url) {
    navigator.clipboard.writeText(url)
      .then(function () { showToast("已复制到剪贴板"); })
      .catch(function () { showToast("复制失败", "error"); });
  }

  function refreshManageData() {
    var key = getManageKey();
    loadStat(key);
    loadList(key);
    manageListLoaded = false;
  }

  /* ── 列表操作事件委托 ─────────────────────────────────── */
  function initListActions() {
    document.addEventListener("click", function (e) {
      var copyBtn = e.target.closest("[data-copy]");
      if (copyBtn) {
        e.preventDefault();
        handleCopy(copyBtn.getAttribute("data-copy"));
        return;
      }
      var deleteBtn = e.target.closest("[data-delete]");
      if (deleteBtn) {
        e.preventDefault();
        handleDelete(deleteBtn.getAttribute("data-delete"));
        return;
      }
    });
  }

  /* ── 管理确认按钮 ─────────────────────────────────────── */
  function handleManageConfirm() {
    var key = getManageKey();
    if (!key) { showToast("请输入 API Key", "error"); return; }
    // 确认后显示统计栏和列表标签页
    var statsBar = $("#manageStatsBar");
    var listTabs = $("#manageListTabs");
    if (statsBar) statsBar.style.display = "";
    if (listTabs) listTabs.style.display = "";
    refreshManageData();
  }

  /* ── 标签页切换（toggle-group 滑动动画风格） ─────────── */
  function initTabs() {
    var group = $("#listToggleGroup");
    if (!group) return;
    var btns = group.querySelectorAll(".toggle-btn");

    btns.forEach(function (btn) {
      btn.addEventListener("click", function () {
        // 更新激活状态
        btns.forEach(function (b) { b.classList.remove("active"); });
        btn.classList.add("active");

        var target = btn.getAttribute("data-tab");
        var isTemp = (target === "temporary");

        // 列表顺序为 [永久 | 临时]，默认高亮左侧（永久）
        // 点击临时时滑块移到右侧
        group.classList.toggle("is-temp", isTemp);

        // 切换面板显示
        document.querySelectorAll("[data-tabpanel]").forEach(function (p) { p.style.display = "none"; });
        var panel = document.querySelector('[data-tabpanel="' + target + '"]');
        if (panel) panel.style.display = "";
      });
    });
  }

  /* ── 工具函数 ──────────────────────────────────────────── */
  function escapeHtml(str) {
    if (!str) return "";
    var div = document.createElement("div");
    div.appendChild(document.createTextNode(str));
    return div.innerHTML;
  }

  function escapeAttr(str) {
    if (!str) return "";
    var d = document.createElement("div");
    d.appendChild(document.createTextNode(str));
    var s = d.innerHTML;
    // d.innerHTML 已转义 & < >，补充转义引号以适配 HTML 属性值
    s = s.replace(/"/g, "\x26quot;");
    s = s.replace(/'/g, "\x26#39;");
    return s;
  }

  function formatExp(isoStr) {
    if (!isoStr || isoStr === "permanent" || isoStr === "0") return "永久";
    try {
      var d = new Date(isoStr);
      if (isNaN(d.getTime())) return isoStr;
      var pad = function (n) { return n < 10 ? "0" + n : "" + n; };
      return d.getFullYear() + "-" + pad(d.getMonth() + 1) + "-" + pad(d.getDate()) +
        " " + pad(d.getHours()) + ":" + pad(d.getMinutes());
    } catch (e) { return isoStr; }
  }

  /* ── 自定义覆盖式滚动条 ─────────────────────────────── */
  function initCustomScrollbar() {
    var track = document.createElement("div");
    track.className = "sb-track";
    var thumb = document.createElement("div");
    thumb.className = "sb-thumb";
    track.appendChild(thumb);
    document.body.appendChild(track);

    var hideTimer = null;

    function show() {
      track.classList.add("show");
      clearTimeout(hideTimer);
    }

    function scheduleHide() {
      clearTimeout(hideTimer);
      hideTimer = setTimeout(function () {
        track.classList.remove("show");
      }, 900);
    }

    function update() {
      var el = document.documentElement;
      var scrollH = el.scrollHeight;
      var clientH = el.clientHeight;
      if (scrollH <= clientH) {
        track.classList.remove("show");
        thumb.style.height = "0px";
        return;
      }
      var ratio = clientH / scrollH;
      var thumbH = Math.max(30, clientH * ratio);
      var scrollTop = el.scrollTop;
      var maxTop = clientH - thumbH;
      var top = maxTop > 0 ? (scrollTop / (scrollH - clientH)) * maxTop : 0;

      thumb.style.height = thumbH + "px";
      thumb.style.transform = "translateY(" + top + "px)";
    }

    window.addEventListener("scroll", function () {
      show();
      update();
      scheduleHide();
    }, { passive: true });

    window.addEventListener("resize", update);

    // 拖拽
    var dragging = false;
    var startY = 0;
    var startScroll = 0;
    var cachedScrollH = 0;
    var cachedClientH = 0;
    var cachedThumbH = 0;

    thumb.addEventListener("mousedown", function (e) {
      e.preventDefault();
      dragging = true;
      startY = e.clientY;
      startScroll = document.documentElement.scrollTop;
      cachedScrollH = document.documentElement.scrollHeight;
      cachedClientH = document.documentElement.clientHeight;
      cachedThumbH = Math.max(30, cachedClientH * (cachedClientH / cachedScrollH));
      thumb.classList.add("active");
      document.body.style.userSelect = "none";
      document.body.style.webkitUserSelect = "none";
    });

    document.addEventListener("mousemove", function (e) {
      if (!dragging) return;
      var dy = e.clientY - startY;
      var maxThumbTop = cachedClientH - cachedThumbH;
      if (maxThumbTop <= 0) return;
      var scrollRatio = dy / maxThumbTop;
      var maxScroll = cachedScrollH - cachedClientH;
      document.documentElement.scrollTop = startScroll + scrollRatio * maxScroll;
      update();
    });

    document.addEventListener("mouseup", function () {
      if (!dragging) return;
      dragging = false;
      thumb.classList.remove("active");
      document.body.style.userSelect = "";
      document.body.style.webkitUserSelect = "";
      scheduleHide();
    });

    // 触摸支持
    thumb.addEventListener("touchstart", function (e) {
      e.preventDefault();
      dragging = true;
      var touch = e.touches[0];
      startY = touch.clientY;
      startScroll = document.documentElement.scrollTop;
      cachedScrollH = document.documentElement.scrollHeight;
      cachedClientH = document.documentElement.clientHeight;
      cachedThumbH = Math.max(30, cachedClientH * (cachedClientH / cachedScrollH));
      thumb.classList.add("active");
    }, { passive: false });

    document.addEventListener("touchmove", function (e) {
      if (!dragging) return;
      var touch = e.touches[0];
      var dy = touch.clientY - startY;
      var maxThumbTop = cachedClientH - cachedThumbH;
      if (maxThumbTop <= 0) return;
      var scrollRatio = dy / maxThumbTop;
      var maxScroll = cachedScrollH - cachedClientH;
      document.documentElement.scrollTop = startScroll + scrollRatio * maxScroll;
      update();
    }, { passive: true });

    document.addEventListener("touchend", function () {
      if (!dragging) return;
      dragging = false;
      thumb.classList.remove("active");
      scheduleHide();
    });

    // 点击轨道跳转
    track.addEventListener("click", function (e) {
      if (e.target === thumb) return;
      var rect = track.getBoundingClientRect();
      var y = e.clientY - rect.top;
      var el = document.documentElement;
      var maxScroll = el.scrollHeight - el.clientHeight;
      if (maxScroll <= 0) return;
      var ratio = y / el.clientHeight;
      el.scrollTop = ratio * maxScroll;
      update();
    });

    // 初始计算
    update();
  }

  /* ── 阻止密码管理器 ───────────────────────────────────── */
  function initPasswordProtection() {
    // readonly 阻止浏览器在页面加载时识别为密码字段
    // 用户聚焦时移除 readonly，允许正常输入
    var inputs = document.querySelectorAll('input[readonly][type="password"]');
    inputs.forEach(function (el) {
      el.addEventListener("mousedown", function () { this.removeAttribute("readonly"); });
      el.addEventListener("touchstart", function () { this.removeAttribute("readonly"); });
      el.addEventListener("focus", function () { this.removeAttribute("readonly"); });
    });
  }

  /* ── 禁止复制和右键 ───────────────────────────────────── */
  function initCopyProtection() {
    // 禁止右键菜单
    document.addEventListener("contextmenu", function (e) {
      e.preventDefault();
    });

    // 禁止复制、剪切
    document.addEventListener("copy", function (e) { e.preventDefault(); });
    document.addEventListener("cut", function (e) { e.preventDefault(); });

    // 禁止相关键盘快捷键
    document.addEventListener("keydown", function (e) {
      // Ctrl/Cmd + C (复制), U (查看源码), S (保存), A (全选), P (打印)
      if ((e.ctrlKey || e.metaKey) && /^[cusap]$/i.test(e.key)) {
        e.preventDefault();
      }
      // F12 (开发者工具)
      if (e.key === "F12") {
        e.preventDefault();
      }
      // Ctrl/Cmd + Shift + I/J/C (开发者工具面板)
      if ((e.ctrlKey || e.metaKey) && e.shiftKey && /^[ijc]$/i.test(e.key)) {
        e.preventDefault();
      }
    });
  }

  /* ── 初始化 ────────────────────────────────────────────── */
  function init() {
    initTheme();
    initPasswordProtection();
    initCopyProtection();
    initCustomScrollbar();
    initToggleGroup();
    initTTLControls();
    initTabs();
    initListActions();

    $("#themeToggle").addEventListener("click", toggleTheme);
    $("#createForm").addEventListener("submit", handleCreate);
    // 成功视图按钮绑定
    var successCopyBtn = $("#successCopyBtn");
    if (successCopyBtn) {
      successCopyBtn.addEventListener("click", copySuccessLink);
    }
    var continueBtn = $("#continueCreateBtn");
    if (continueBtn) {
      continueBtn.addEventListener("click", hideSuccessView);
    }
    var confirmBtn = $("#manageConfirmBtn");
    if (confirmBtn) {
      confirmBtn.addEventListener("click", handleManageConfirm);
    }

    // 刷新按钮绑定
    var refreshBtn = $("#listRefreshBtn");
    if (refreshBtn) {
      refreshBtn.addEventListener("click", function () {
        refreshManageData();
        showToast("列表已刷新");
      });
    }
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
