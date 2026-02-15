// web/shared/utils.js — shared dashboard utilities (ES module)

export function clearChildren(el) {
  while (el.firstChild) el.removeChild(el.firstChild);
}

export function isMobileLayout() {
  return window.matchMedia('(max-width: 900px)').matches;
}

export function openDrawer() {
  if (!isMobileLayout()) return;
  document.body.classList.add('drawer-open');
}

export function closeDrawer() {
  document.body.classList.remove('drawer-open');
}

export function bindMobileLayoutEvents(navBtn, closeBtn, backdrop) {
  if (navBtn) navBtn.addEventListener('click', function() { openDrawer(); });
  if (closeBtn) closeBtn.addEventListener('click', function() { closeDrawer(); });
  if (backdrop) backdrop.addEventListener('click', function() { closeDrawer(); });
}

export function createBadge(text, classNames) {
  var el = document.createElement('span');
  el.textContent = text;
  el.className = classNames;
  return el;
}

export function formatTime(ts) {
  if (!ts) return '';
  var d = new Date(ts);
  if (isNaN(d.getTime())) return '';
  return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

export function truncate(str, max) {
  if (!str || str.length <= max) return str;
  return str.substring(0, max) + '...';
}
