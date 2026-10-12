import type { Cell, Status } from "./api";

// Index2's order and textures: Bithumb solid, Upbit outline.
export const EXCHANGES = [
  ["bithumb", "빗썸"],
  ["upbit", "업비트"],
] as const;

export const TEXTURES = ["solid", "outline"] as const;

export type ExchangeId = (typeof EXCHANGES)[number][0];

export const EXCHANGE_NAME: Record<string, string> = Object.fromEntries(EXCHANGES);

/** Two decimals; three only when two would show 0.00. */
export function formatShare(value: number | null | undefined): string {
  if (value == null || !Number.isFinite(value)) return "—";
  if (value > 100.0001) return "확인 필요";
  const digits = value > 0 && value < 0.005 ? 3 : 2;
  return `${value.toFixed(digits)}%`;
}

/** Won amounts in 억 원 with the chosen decimals, as in Index2. */
export function formatEok(value: number | null | undefined, decimals: number): string {
  if (value == null || !Number.isFinite(value)) return "—";
  return (value / 1e8).toLocaleString("ko-KR", { minimumFractionDigits: decimals, maximumFractionDigits: decimals });
}

/** A short readable won amount for summaries. */
export function formatKRW(value: number | null | undefined): string {
  if (value == null || !Number.isFinite(value)) return "—";
  if (value >= 1e12) return `${(value / 1e12).toLocaleString("ko-KR", { maximumFractionDigits: 3 })}조 원`;
  if (value >= 1e8) return `${(value / 1e8).toLocaleString("ko-KR", { maximumFractionDigits: 2 })}억 원`;
  if (value >= 1e4) return `${Math.round(value / 1e4).toLocaleString("ko-KR")}만 원`;
  if (value > 0) return "1만 원 미만";
  return "0원";
}

export function formatChange(value: number | null | undefined): string {
  if (value == null || !Number.isFinite(value)) return "—";
  const sign = value > 0 ? "+" : "";
  return `${sign}${value.toLocaleString("ko-KR", { maximumFractionDigits: 1, minimumFractionDigits: 1 })}%`;
}

const kst = new Intl.DateTimeFormat("ko-KR", {
  timeZone: "Asia/Seoul", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false,
});
const kstTime = new Intl.DateTimeFormat("ko-KR", { timeZone: "Asia/Seoul", hour: "2-digit", minute: "2-digit", hour12: false });

export function formatKST(ms: number | null | undefined): string {
  return ms ? kst.format(new Date(ms)) : "—";
}

export function formatKSTTime(ms: number | null | undefined): string {
  return ms ? kstTime.format(new Date(ms)) : "—";
}

export function formatElapsed(fromMs: number | null | undefined, nowMs: number): string {
  if (!fromMs) return "—";
  const minutes = Math.max(0, Math.floor((nowMs - fromMs) / 60_000));
  const hours = Math.floor(minutes / 60);
  if (hours >= 24) return `${Math.floor(hours / 24)}일 ${hours % 24}시간`;
  return hours ? `${hours}시간 ${minutes % 60}분` : `${minutes}분`;
}

export const STATUS_LABEL: Record<Status, string> = {
  FINAL: "확정",
  LIVE: "진행 중",
  PROVISIONAL: "확정 중",
  PARTIAL: "일부 미확인",
  PENDING: "대기",
  WAITING: "첫 체결 대기",
  UNLISTED: "미상장",
};

export function statusLabel(cell: Cell | undefined): string {
  return cell ? STATUS_LABEL[cell.status] ?? cell.status : "—";
}

export const STATE_LABEL: Record<string, string> = {
  WAITING: "첫 체결 대기",
  TRACKING: "추적 중",
  COMPLETE: "24시간 완료",
};
