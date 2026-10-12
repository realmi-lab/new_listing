import { useEffect, useId, useRef, useState } from "react";
import { createPortal } from "react-dom";

export function FilterButton({label, value, options, onChange, disabled = false}: {
  label: string; value: string; options: ReadonlyArray<readonly [string,string]>;
  onChange: (value: string) => void; disabled?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [position, setPosition] = useState({left: 0, top: 0});
  const trigger = useRef<HTMLButtonElement>(null), menu = useRef<HTMLDivElement>(null);
  const id = useId();
  const selected = options.find(([key]) => key === value)?.[1] ?? value;
  useEffect(() => {
    if (!open) return;
    const outside = (e: PointerEvent) => { if (!trigger.current?.contains(e.target as Node) && !menu.current?.contains(e.target as Node)) setOpen(false); };
    const escape = (e: KeyboardEvent) => { if (e.key === "Escape") { setOpen(false); trigger.current?.focus(); } };
    const close = (e: Event) => { if (!(e.target instanceof Node) || !menu.current?.contains(e.target)) setOpen(false); };
    const reposition = () => { const rect = trigger.current?.getBoundingClientRect(); if (rect) setPosition({left: Math.max(8, Math.min(rect.left, innerWidth - 208)), top: Math.max(8, Math.min(rect.bottom + 4, innerHeight - 208))}); };
    document.addEventListener("pointerdown", outside); document.addEventListener("keydown", escape);
    window.addEventListener("resize", close); window.addEventListener("scroll", reposition, true);
    return () => { document.removeEventListener("pointerdown", outside); document.removeEventListener("keydown", escape); window.removeEventListener("resize", close); window.removeEventListener("scroll", reposition, true); };
  }, [open]);
  const show = () => {
    const rect = trigger.current?.getBoundingClientRect(); if (!rect) return;
    setPosition({left: Math.max(8, Math.min(rect.left, innerWidth - 208)), top: Math.max(8, Math.min(rect.bottom + 4, innerHeight - 208))});
    setOpen(v => !v);
  };
  return <>
    <button ref={trigger} type="button" className="nl-filter-button" aria-label={label} aria-haspopup="listbox" aria-expanded={open} aria-controls={open ? id : undefined} disabled={disabled} title={`${label}: ${selected}`} onClick={show} onKeyDown={e => { if(e.key === "ArrowDown") { e.preventDefault(); show(); setTimeout(() => menu.current?.querySelector<HTMLButtonElement>("button")?.focus(), 0); } }}>{label}</button>
    {open && createPortal(<div ref={menu} id={id} className="nl-filter-list" role="listbox" aria-label={`${label} 선택`} style={position} onKeyDown={e => {
      if(e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
      e.preventDefault(); const buttons = [...(menu.current?.querySelectorAll<HTMLButtonElement>("button") ?? [])];
      const index = buttons.indexOf(document.activeElement as HTMLButtonElement);
      buttons[(index + (e.key === "ArrowDown" ? 1 : -1) + buttons.length) % buttons.length]?.focus();
    }}>{options.map(([key,text]) => <button type="button" role="option" aria-selected={key === value} key={key} onClick={() => { onChange(key); setOpen(false); trigger.current?.focus(); }}>{text}</button>)}</div>, document.body)}
  </>;
}
