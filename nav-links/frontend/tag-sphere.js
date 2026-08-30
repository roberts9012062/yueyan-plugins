// nav-links/frontend/tag-sphere.js
// 3D 球形标签云组件（原生 ESM，无依赖；Fibonacci 球面分布 + rAF 旋转投影）。
// 交互：自动缓慢旋转；悬停减速；按住拖拽旋转（点击与拖拽自动区分）；点击标签回调筛选。
// createTagSphere({ mount, tags, onSelect, active }) → { destroy, setActive }
//   tags: [{name, count}]；onSelect(name)；active: 当前选中标签名（高亮实底）。
import { escapeHtml } from "/plugin-sdk/shared.js";

// sphereConfig 球体参数（半径按容器尺寸自适应，此处为比例系数）。
const sphereConfig = {
  radiusRatio: 0.38, // 球半径 = min(宽,高) * 系数
  autoSpeed: 0.0038, // 自动旋转角速度（弧度/帧）
  tilt: 0.42, // 固定 X 轴倾角（弧度，立体观感）
  maxTags: 24, // 标签数量上限（过多球面拥挤，按使用计数截断）
};

// fibonacciSphere 生成球面均匀分布坐标（纯函数；返回 [{x,y,z}]，分量 ∈ [-1,1]）。
function fibonacciSphere(count) {
  const points = [];
  const golden = Math.PI * (3 - Math.sqrt(5)); // 黄金角
  for (let i = 0; i < count; i++) {
    const y = 1 - ((i + 0.5) * 2) / count;
    const r = Math.sqrt(Math.max(0, 1 - y * y));
    const phi = i * golden;
    points.push({ x: Math.cos(phi) * r, y: y, z: Math.sin(phi) * r });
  }
  return points;
}

export function createTagSphere(opts) {
  const mount = opts.mount;
  const tags = (opts.tags || []).slice(0, sphereConfig.maxTags);
  const coords = fibonacciSphere(tags.length);
  let angle = 0; // 当前 Y 轴旋转角
  let tilt = sphereConfig.tilt;
  let hover = false;
  let dragging = false;
  let moved = false; // 本次按下是否发生拖拽（区分点击）
  let lastX = 0;
  let lastY = 0;
  let speed = sphereConfig.autoSpeed; // 当前角速度（悬停时向 0 缓动）
  let rafId = 0;
  let active = opts.active || "";

  const els = tags.map((t, i) => {
    const btn = document.createElement("button");
    btn.type = "button";
    btn.dataset.tagName = t.name;
    btn.title = t.name + " · " + (t.count || 0) + " 个站点";
    btn.innerHTML = escapeHtml(t.name) + '<span class="nav-sphere-count">' + (t.count || 0) + "</span>";
    btn.className = "nav-sphere-tag";
    mount.appendChild(btn);
    return btn;
  });

  // project 单帧投影：绕 Y 旋转 → 绕 X 倾斜 → 屏幕坐标 + 深度缩放/透明度。
  const render = () => {
    const w = mount.clientWidth || 240;
    const h = mount.clientHeight || 260;
    const cx = w / 2;
    const cy = h / 2;
    const R = Math.min(w, h) * sphereConfig.radiusRatio + (tags.length > 12 ? 14 : 22);
    const cosT = Math.cos(tilt);
    const sinT = Math.sin(tilt);
    const cosA = Math.cos(angle);
    const sinA = Math.sin(angle);
    els.forEach((el, i) => {
      const p = coords[i];
      // 绕 Y 轴旋转
      const x1 = p.x * cosA - p.z * sinA;
      const z1 = p.x * sinA + p.z * cosA;
      // 绕 X 轴倾斜（固定倾角）
      const y2 = p.y * cosT - z1 * sinT;
      const z2 = p.y * sinT + z1 * cosT;
      const scale = 0.6 + 0.4 * (z2 + 1) / 2; // 0.6 ~ 1.0
      el.style.transform =
        "translate(-50%,-50%) translate3d(" + (cx + x1 * R).toFixed(1) + "px," + (cy - y2 * R).toFixed(1) + "px,0) scale(" + scale.toFixed(3) + ")";
      el.style.opacity = (0.35 + 0.65 * (z2 + 1) / 2).toFixed(2);
      el.style.zIndex = String(Math.round((z2 + 1) * 50));
    });
  };

  const loop = () => {
    // 悬停/拖拽时角速度缓动到 0，离开缓动回自动速度
    const target = dragging || hover ? 0 : sphereConfig.autoSpeed;
    speed += (target - speed) * 0.08;
    if (!dragging) {
      angle += speed;
    }
    render();
    rafId = requestAnimationFrame(loop);
  };

  // ---------- 交互事件 ----------
  const onEnter = () => (hover = true);
  const onLeave = () => {
    hover = false;
    dragging = false;
  };
  const onDown = (ev) => {
    dragging = true;
    moved = false;
    lastX = ev.clientX;
    lastY = ev.clientY;
  };
  const onMove = (ev) => {
    if (!dragging) {
      return;
    }
    const dx = ev.clientX - lastX;
    const dy = ev.clientY - lastY;
    if (Math.abs(dx) + Math.abs(dy) > 3) {
      moved = true;
    }
    angle += dx * 0.006;
    tilt = Math.max(-0.9, Math.min(0.9, tilt + dy * 0.005));
    lastX = ev.clientX;
    lastY = ev.clientY;
  };
  const onUp = () => (dragging = false);
  const onTagClick = (ev) => {
    const btn = ev.target.closest("[data-tag-name]");
    if (!btn || moved) {
      return; // 拖拽结束不触发点击
    }
    if (typeof opts.onSelect === "function") {
      opts.onSelect(btn.dataset.tagName);
    }
  };

  mount.classList.add("nav-sphere");
  mount.addEventListener("mouseenter", onEnter);
  mount.addEventListener("mouseleave", onLeave);
  mount.addEventListener("pointerdown", onDown);
  mount.addEventListener("pointermove", onMove);
  mount.addEventListener("pointerup", onUp);
  mount.addEventListener("pointercancel", onUp);
  mount.addEventListener("click", onTagClick);

  // setActive 更新选中高亮（activeTag 为空串表示无选中）。
  const setActive = (activeTag) => {
    active = activeTag || "";
    els.forEach((el) => {
      el.classList.toggle("nav-sphere-tag-active", el.dataset.tagName === active);
    });
  };
  setActive(active);
  loop();

  // destroy 停止动画并清理全部监听（卸载/重渲染时由调用方执行）。
  const destroy = () => {
    cancelAnimationFrame(rafId);
    mount.classList.remove("nav-sphere");
    mount.removeEventListener("mouseenter", onEnter);
    mount.removeEventListener("mouseleave", onLeave);
    mount.removeEventListener("pointerdown", onDown);
    mount.removeEventListener("pointermove", onMove);
    mount.removeEventListener("pointerup", onUp);
    mount.removeEventListener("pointercancel", onUp);
    mount.removeEventListener("click", onTagClick);
    mount.innerHTML = "";
  };

  return { destroy: destroy, setActive: setActive };
}
