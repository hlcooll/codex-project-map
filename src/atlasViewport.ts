export function zoomScroll(scroll: { left: number; top: number }, anchor: { x: number; y: number }, before: number, after: number) {
  return { left: (scroll.left + anchor.x) * after / before - anchor.x, top: (scroll.top + anchor.y) * after / before - anchor.y }
}
export function fitZoom(content: { width: number; height: number }, viewport: { width: number; height: number }) {
  return Math.max(.5, Math.min(1, (viewport.width - 32) / content.width, (viewport.height - 32) / content.height))
}
