import { useEffect, useState } from "react";

export const CONTRACT_VERSION = "LISTING_TRACKER_V2";

export type Status = "FINAL" | "LIVE" | "PROVISIONAL" | "PARTIAL" | "PENDING" | "WAITING" | "UNLISTED";

export interface Cell {
  amount: number | null;
  share: number | null;
  status: Status;
}

export interface Hour {
  index: number;
  from: number;
  to: number;
  status: Status;
  total: Cell;
  exchanges: Record<string, Cell>;
  cumulative: number | null;
  exchange_cumulative: Record<string, number | null> | null;
  change_pct: number | null;
}

export interface ExchangeInfo {
  listed: boolean;
  market?: string;
  newly_listed: boolean;
  relisting: boolean;
  first_trade_at: number | null;
  detected_at: number | null;
  source?: string;
  final_through: number | null;
  last_price: number | null;
  last_trade_at: number | null;
  last_success_at: number | null;
  last_error?: string;
  cumulative: Cell;
}

export interface NoticeInfo {
  exchange: string;
  title: string;
  url: string;
  category: string;
  published_at: number | null;
  source: string;
  krw: boolean;
  symbols: string[] | null;
  coin?: string;
  coin_state?: string;
}

export type CoinState = "WAITING" | "TRACKING" | "COMPLETE";

export interface Coin {
  key: string;
  base: string;
  korean_name: string;
  english_name: string;
  source: string;
  state: CoinState;
  anchor_at: number | null;
  end_at: number | null;
  current_hour: number;
  exchanges: Record<string, ExchangeInfo>;
  hours: Hour[] | null;
  cumulative: Cell;
  first_hour: Cell;
  last_hour: Cell;
  recent_hour: Cell;
  recent_hour_from?: number;
  notices: NoticeInfo[] | null;
}

export interface Progress {
  total: number;
  done: number;
  found: number;
  errors: number;
  running: boolean;
  last_error?: string;
}

export interface Feeds {
  registry: Record<string, { last_success_at: string; last_error?: string; active_markets: number }>;
  discovery: Record<string, Progress>;
  notices: Record<string, { mode: string; last_success_at: string; last_error?: string }>;
  clock_offset_ms: number;
  upbit_key: boolean;
  version: string;
  started_at: number;
}

export interface Snapshot {
  contract_version: string;
  generated_at: number;
  lookback_sec: number;
  track_hours: number;
  overview: {
    tracking: number;
    waiting: number;
    complete: number;
    cumulative: Cell;
    exchanges: Record<string, Cell>;
  };
  coins: Coin[] | null;
  notices: NoticeInfo[] | null;
  feeds?: Feeds;
}

function valid(payload: unknown): payload is Snapshot {
  const value = payload as Snapshot | null;
  return value?.contract_version === CONTRACT_VERSION && typeof value.generated_at === "number" && typeof value.overview === "object";
}

export type Connection = "connecting" | "live" | "polling" | "offline";

/**
 * Follows the program's once-a-second event stream. When the stream fails it
 * reads the snapshot every two seconds and retries the stream later.
 */
export function useSnapshot(): { data: Snapshot | null; connection: Connection; error: string } {
  const [data, setData] = useState<Snapshot | null>(null);
  const [connection, setConnection] = useState<Connection>("connecting");
  const [error, setError] = useState("");

  useEffect(() => {
    let closed = false;
    let source: EventSource | null = null;
    let poll: number | undefined;
    let retry: number | undefined;

    const accept = (payload: unknown) => {
      if (!valid(payload)) {
        setError("받은 자료의 형식을 확인할 수 없습니다. 프로그램과 화면의 버전이 같은지 확인하세요.");
        return;
      }
      setData(payload);
      setError("");
    };

    const startPolling = () => {
      if (poll !== undefined) return;
      const read = async () => {
        try {
          const response = await fetch("/api/snapshot", { headers: { Accept: "application/json" }, cache: "no-store" });
          if (!response.ok) throw new Error(String(response.status));
          accept(await response.json());
          if (!closed) setConnection("polling");
        } catch {
          if (!closed) setConnection("offline");
        }
      };
      void read();
      poll = window.setInterval(read, 2000);
    };

    const openStream = () => {
      if (closed) return;
      source = new EventSource("/api/stream");
      source.onmessage = event => {
        try {
          accept(JSON.parse(event.data));
          setConnection("live");
          if (poll !== undefined) {
            window.clearInterval(poll);
            poll = undefined;
          }
        } catch {
          setError("받은 자료를 읽지 못했습니다.");
        }
      };
      source.onerror = () => {
        source?.close();
        source = null;
        startPolling();
        retry = window.setTimeout(openStream, 10_000);
      };
    };
    openStream();
    return () => {
      closed = true;
      source?.close();
      if (poll !== undefined) window.clearInterval(poll);
      if (retry !== undefined) window.clearTimeout(retry);
    };
  }, []);

  return { data, connection, error };
}
