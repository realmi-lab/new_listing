import { useMemo, useState } from "react";

import { useSnapshot, type Cell, type Coin, type Hour, type NoticeInfo, type Snapshot } from "./api";
import { FilterButton } from "./FilterButton";
import {
  EXCHANGES, EXCHANGE_NAME, STATE_LABEL, TEXTURES, formatChange, formatEok, formatElapsed, formatKRW,
  formatKST, formatKSTTime, formatShare, statusLabel,
} from "./format";
import { applyTheme, savedTheme, type Theme } from "./theme";

type View = "coins" | "hours" | "notices";
type SortField = "listed" | "name" | "exchange" | "cumulative" | "recent" | "current" | "change" | `ex:${string}`;
type Sort = { field: SortField; ascending: boolean } | null;

const ROW_STEP = 100;

function SortHeading({ label, field, sort, onSort, className }: {
  label: string; field: SortField; sort: Sort; onSort: (field: SortField) => void; className?: string;
}) {
  const direction = sort?.field === field ? sort.ascending : undefined;
  return <th className={className} aria-sort={direction == null ? "none" : direction ? "ascending" : "descending"}>
    <button type="button" onClick={() => onSort(field)}>{label} <span className="nl-sort-arrow">{direction == null ? "↕" : direction ? "↑" : "↓"}</span></button>
  </th>;
}

function TextureBar({ segments, label, large = false }: {
  segments: Array<{ exchange: string; share: number | null }>; label: string; large?: boolean;
}) {
  const visible = segments.filter(segment => segment.share != null && segment.share > 0);
  return <div className={`nl-bar${large ? " nl-bar-large" : ""}`} role="img" aria-label={label}>
    {visible.map(segment => {
      const index = EXCHANGES.findIndex(([id]) => id === segment.exchange);
      return <span key={segment.exchange} className={`nl-texture nl-texture-${TEXTURES[Math.max(0, index)]} nl-ex-${segment.exchange}`}
        style={{ width: `${segment.share}%` }} title={`${EXCHANGE_NAME[segment.exchange]} ${formatShare(segment.share)}`} />;
    })}
    {!visible.length && <span className="nl-bar-empty">자료 확인 중</span>}
  </div>;
}

function cellSegments(cells: Record<string, Cell> | undefined) {
  return EXCHANGES.map(([exchange]) => ({ exchange, share: cells?.[exchange]?.share ?? null }));
}

function coinExchangeCells(coin: Coin): Record<string, Cell> {
  return Object.fromEntries(EXCHANGES.map(([id]) => [id, coin.exchanges[id]?.cumulative]));
}

function currentHour(coin: Coin): Hour | undefined {
  const hours = coin.hours ?? [];
  if (coin.state === "COMPLETE") return hours[hours.length - 1];
  return hours[Math.min(Math.max(coin.current_hour, 1), hours.length) - 1];
}

/** The latest whole hour's change against the hour before it. */
function lastChange(coin: Coin): number | null {
  const hours = (coin.hours ?? []).filter(hour => hour.status !== "PENDING" && hour.status !== "LIVE");
  return hours.length ? hours[hours.length - 1].change_pct : null;
}

/** Started but some market has not been read once yet. */
function stillLoading(coin: Coin): boolean {
  return coin.state !== "WAITING" && EXCHANGES.some(([id]) => coin.exchanges[id]?.listed && coin.exchanges[id]?.last_success_at == null);
}

/** The coin's state as shown: past its 24 hours but not all re-read yet is "확정 중". */
function stateLabel(coin: Coin, now: number): string {
  if (coin.state === "TRACKING" && coin.end_at != null && now >= coin.end_at) return "24시간 확정 중";
  return STATE_LABEL[coin.state];
}

function elapsedLabel(coin: Coin, now: number): string {
  if (coin.state === "COMPLETE" || (coin.end_at != null && now >= coin.end_at)) return "24시간";
  return formatElapsed(coin.anchor_at, now);
}

function listingLabel(coin: Coin): string {
  const exchanges = EXCHANGES.filter(([id]) => coin.exchanges[id]?.newly_listed).map(([, name]) => name);
  if (coin.source === "MANUAL") return "직접";
  return exchanges.length ? exchanges.join("·") : "—";
}

/** "[신규상장]" for the exchange that newly listed the coin, "[기존]" for one that already had it. */
function kindLabel(coin: Coin, id: string): string {
  const info = coin.exchanges[id];
  if (!info?.listed) return "[미상장]";
  if (coin.source === "MANUAL") return "[직접]";
  return info.newly_listed ? (info.relisting ? "[재상장]" : "[신규상장]") : "[기존]";
}

/** Where the coin is newly listed, each exchange in its color. */
function ExchangeTag({ coin }: { coin: Coin }) {
  if (coin.source === "MANUAL") return <span className="nl-ex-tag is-manual">직접</span>;
  const listed = EXCHANGES.filter(([id]) => coin.exchanges[id]?.newly_listed);
  if (!listed.length) return <span className="nl-ex-tag">—</span>;
  return <span className="nl-ex-tag">{listed.map(([id, name], i) =>
    <span key={id}>{i > 0 && "·"}<span className={`nl-ex-${id}`}>{name}</span></span>)}</span>;
}

/** Outside the tables the exchange is written before the coin. */
function CoinName({ coin }: { coin: Coin }) {
  return <><ExchangeTag coin={coin} /> {coin.base}</>;
}

function sortValue(coin: Coin, field: SortField): number | string | null {
  switch (field) {
    case "name": return coin.base;
    case "exchange": return listingLabel(coin);
    case "listed": return coin.anchor_at;
    case "cumulative": return coin.cumulative.amount;
    case "recent": return coin.recent_hour.amount;
    case "current": return currentHour(coin)?.total.amount ?? null;
    case "change": return lastChange(coin);
    default: return coin.exchanges[field.slice(3)]?.cumulative.amount ?? null;
  }
}

function useCoins(data: Snapshot | null, query: string) {
  const [listing, setListing] = useState("all");
  const [state, setState] = useState("all");
  const [sort, setSort] = useState<Sort>(null);
  const coins = useMemo(() => {
    const q = query.trim().toLowerCase();
    const rows = (data?.coins ?? []).filter(coin => {
      if (q && !`${coin.base} ${coin.korean_name} ${coin.english_name}`.toLowerCase().includes(q)) return false;
      if (state !== "all" && coin.state !== state) return false;
      if (listing === "manual") return coin.source === "MANUAL";
      if (listing !== "all" && !coin.exchanges[listing]?.newly_listed) return false;
      return true;
    });
    if (sort) rows.sort((a, b) => {
      const x = sortValue(a, sort.field), y = sortValue(b, sort.field);
      if (x == null || y == null) return Number(x == null) - Number(y == null);
      const order = typeof x === "string" ? x.localeCompare(String(y)) : x - (y as number);
      return (sort.ascending ? 1 : -1) * order || a.base.localeCompare(b.base);
    });
    return rows;
  }, [data, query, listing, state, sort]);
  const onSort = (field: SortField) => setSort(s => ({ field, ascending: s?.field === field ? !s.ascending : field === "name" }));
  return { coins, listing, setListing, state, setState, sort, setSort, onSort };
}

/** One short collection status; the per-exchange details stay in its tooltip. */
function collectionStatus(data: Snapshot | null): { text: string; alert: boolean; detail: string } {
  const feeds = data?.feeds;
  if (!feeds) return { text: "연결 중", alert: false, detail: "" };
  const ok = (iso?: string) => iso && !iso.startsWith("0001") ? formatKSTTime(Date.parse(iso)) : "—";
  const detail = [
    `마켓 목록: ${EXCHANGES.map(([id, name]) => `${name} ${feeds.registry[id]?.last_error ? "오류" : ok(feeds.registry[id]?.last_success_at)}`).join(", ")}`,
    `최근 상장 찾기: ${EXCHANGES.map(([id, name]) => {
      const p = feeds.discovery[id];
      return !p ? `${name} —` : p.running ? `${name} ${p.done}/${p.total}` : `${name} ${p.found}개${p.errors ? `, 실패 ${p.errors}` : ""}`;
    }).join(", ")}`,
    `상장 공지: ${EXCHANGES.map(([id, name]) => {
      const n = feeds.notices[id];
      return `${name}(${n?.mode === "WEBSOCKET" ? "웹소켓" : "목록"}) ${n?.last_error ? "오류" : ok(n?.last_success_at)}`;
    }).join(", ")}`,
  ].join("\n");
  const failed = EXCHANGES.some(([id]) => feeds.registry[id]?.last_error || feeds.notices[id]?.last_error || feeds.discovery[id]?.errors);
  const scanning = EXCHANGES.map(([id]) => feeds.discovery[id]).filter(p => p?.running);
  if (scanning.length) {
    const done = scanning.reduce((n, p) => n + p.done, 0), total = scanning.reduce((n, p) => n + p.total, 0);
    return { text: `최근 상장 찾는 중 ${done}/${total}`, alert: false, detail };
  }
  if (failed) return { text: "일부 수집 오류", alert: true, detail };
  return { text: `정상 · ${formatKSTTime(data?.generated_at)} 갱신`, alert: false, detail };
}

export default function App() {
  const { data, connection, error } = useSnapshot();
  const [view, setView] = useState<View>("coins");
  const [query, setQuery] = useState("");
  const [decimals, setDecimals] = useState(2);
  const [hourMode, setHourMode] = useState<"hour" | "cumulative">("hour");
  const [selected, setSelected] = useState<string | null>(null);
  const [rowLimit, setRowLimit] = useState(ROW_STEP);
  const [theme, setTheme] = useState<Theme>(savedTheme);
  const toggleTheme = () => setTheme(current => {
    const next = current === "dark" ? "light" : "dark";
    applyTheme(next);
    return next;
  });
  const filters = useCoins(data, query);
  const now = data?.generated_at ?? Date.now();
  // Every amount on the screen is in 억 원 and says so.
  const amount = (value: number | null | undefined) => {
    const text = formatEok(value, decimals);
    return text === "—" ? text : `${text}억`;
  };
  const lookbackDays = data ? Math.round(data.lookback_sec / 86400) : 7;

  const selectedCoin = (data?.coins ?? []).find(coin => coin.key === selected) ?? filters.coins.find(coin => coin.state !== "WAITING") ?? null;
  const visible = filters.coins.slice(0, rowLimit);
  const overview = data?.overview;
  const connectionLabel = { connecting: "연결 중", live: "실시간", polling: "2초마다 확인", offline: "연결 끊김" }[connection];
  const status = collectionStatus(data);

  const controls = <div className="nl-filters" aria-label="코인 필터">
    <FilterButton label="상장 거래소" value={filters.listing} onChange={filters.setListing}
      options={[["all", "전체"], ["bithumb", "빗썸 신규"], ["upbit", "업비트 신규"], ["manual", "직접 추가"]]} />
    <FilterButton label="상태" value={filters.state} onChange={filters.setState}
      options={[["all", "전체 상태"], ["TRACKING", "추적 중"], ["COMPLETE", "24시간 완료"], ["WAITING", "첫 체결 대기"]]} />
    {view === "hours" && <FilterButton label="시간별 값" value={hourMode} onChange={value => setHourMode(value as "hour" | "cumulative")}
      options={[["hour", "그 시간 거래금액"], ["cumulative", "상장 후 누적"]]} />}
    <FilterButton label="거래금액 소수점" value={String(decimals)} onChange={value => setDecimals(Number(value))}
      options={[0, 1, 2, 3, 4].map(n => [String(n), `${n}자리`] as const)} />
    <button type="button" onClick={() => { filters.setListing("all"); filters.setState("all"); filters.setSort(null); setDecimals(2); setQuery(""); }}>필터·정렬 초기화</button>
    <span role="status">{data ? `코인 ${filters.coins.length}개` : "자료를 기다리는 중…"}</span>
  </div>;

  return (
    <main className="nl-terminal" aria-labelledby="nl-title">
      <aside className="nl-pane nl-overview">
        <div className="nl-scroll nl-overview-content">
          <h1 id="nl-title" className="nl-ascii" aria-label="신규 상장 24시간 추적">
            {`/ \\ / \\ / \\ / \\ / \\\n+ N E W   L I S T\n+ T R A C K  2 4 H\n+\\_/ \\_/ \\_/ \\_/ \\_/`}
          </h1>
          <p className="nl-copy">업비트·빗썸 신규 상장 코인의 상장 후 24시간 거래금액</p>
          <p className="nl-source">최근 {lookbackDays}일 상장 · 1분봉 기준</p>
          <div className="nl-divider" aria-hidden="true">----------------------------------------</div>
          <div className="nl-summary-heading">
            <span>추적 중 거래금액</span>
            <strong>{amount(overview?.cumulative.amount)}</strong>
          </div>
          <TextureBar segments={cellSegments(overview?.exchanges)} label="추적 중 코인의 거래소별 비중" large />
          <ul className="nl-legend" aria-label="거래소별 비중 범례">
            {EXCHANGES.map(([id, name], index) => <li key={id}>
              <span className={`nl-legend-box nl-texture nl-texture-${TEXTURES[index]} nl-ex-${id}`} />
              <span>{formatShare(overview?.exchanges[id]?.share)}</span>
              <span>{name}</span>
            </li>)}
          </ul>
          <div className="nl-counts">
            <span>추적 중 <strong>{overview?.tracking ?? 0}</strong></span>
            <span>대기 <strong>{overview?.waiting ?? 0}</strong></span>
            <span>완료 <strong>{overview?.complete ?? 0}</strong></span>
          </div>
          {controls}
          <div className="nl-status" title={status.detail}>
            상태 <span className={connection === "offline" || status.alert ? "is-alert" : ""}>{connection === "live" ? "실시간" : connectionLabel}</span>
            <small>{status.text}</small>
          </div>
          <button type="button" className="nl-theme-toggle" onClick={toggleTheme} aria-pressed={theme === "light"}>
            {theme === "dark" ? "밝은 화면으로" : "어두운 화면으로"}
          </button>
        </div>
      </aside>

      <section className="nl-pane nl-detail" aria-label="신규 상장 표">
        <div className="nl-scroll">
          <div className="nl-toolbar">
            <div className="nl-view-switch" aria-label="보기 방식">
              {([["coins", "코인 보기"], ["hours", "시간별 보기"], ["notices", "상장 공지"]] as const).map(([id, label]) =>
                <button key={id} type="button" className={view === id ? "is-selected" : ""} onClick={() => setView(id)}>{label}</button>)}
            </div>
            <label className="nl-search">
              <span>[ 코인 검색 ]</span>
              <input type="search" aria-label="코인 검색" value={query} disabled={view === "notices"}
                onChange={event => { setQuery(event.target.value); setRowLimit(ROW_STEP); }} />
            </label>
          </div>
          <p className="nl-copy nl-detail-copy">
            {view === "coins" && "누적은 첫 체결부터 지금(최대 24시간)까지, 최근 1시간은 지금까지의 직전 60분, 현재 시간은 첫 체결 기준으로 진행 중인 1시간입니다."}
            {view === "hours" && `+1시간은 첫 체결 시각부터 60분입니다. ${hourMode === "hour" ? "각 칸은 그 1시간의 거래금액" : "각 칸은 상장 후 그 시간까지의 누적 거래금액"}입니다.`}
            {view === "notices" && `최근 ${lookbackDays}일의 상장 관련 공지입니다. 공지는 예고이고, 실제 상장 시각은 마켓 목록과 첫 1분봉으로 확인합니다.`}
          </p>
          {error && <p className="nl-alert" role="alert">{error}</p>}
          {connection === "offline" && <p className="nl-alert" role="alert">프로그램과 연결이 끊겼습니다. 프로그램 창이 열려 있는지 확인하세요. 마지막으로 받은 자료를 표시합니다.</p>}

          <div className="nl-table-wrap" tabIndex={0} aria-label="표 가로 스크롤">
            {view === "coins" && <CoinTable coins={visible} total={filters.coins.length} sort={filters.sort} onSort={filters.onSort}
              amount={amount} now={now} selected={selectedCoin?.key ?? null} onSelect={setSelected} loading={!data} />}
            {view === "hours" && <HourTable coins={visible} mode={hourMode} amount={amount} selected={selectedCoin?.key ?? null} onSelect={setSelected} loading={!data} />}
            {view === "notices" && <NoticeTable notices={data?.notices ?? []} loading={!data} />}
          </div>
          {view !== "notices" && filters.coins.length > visible.length && <div className="nl-more">
            <span>{filters.coins.length.toLocaleString("ko-KR")}개 중 {visible.length.toLocaleString("ko-KR")}개 표시</span>
            <button type="button" onClick={() => setRowLimit(limit => limit + ROW_STEP)}>{ROW_STEP}개 더 보기</button>
            <button type="button" onClick={() => setRowLimit(filters.coins.length)}>전체 보기</button>
          </div>}
          {data && <div className="nl-table-end" aria-hidden="true">........................................<br />표 끝</div>}
        </div>
      </section>

      <aside className="nl-pane nl-visual" aria-label="선택한 코인의 시간별 거래금액">
        <div className="nl-scroll nl-visual-scroll">
          <CoinDetail coin={selectedCoin} amount={amount} now={now} />
          <div className="nl-visual-heading"><span>상장 코인 24시간</span></div>
          <div className="nl-visual-legend">[ 그리드 범례 ]<br />막대 = +1~+24시간 거래금액<br />무늬 = 빗썸 · 업비트 구성</div>
          <div className="nl-grid">
            {filters.coins.filter(coin => coin.hours).slice(0, 24).map(coin =>
              <button type="button" key={coin.key} className={`nl-grid-card${coin.key === selectedCoin?.key ? " is-selected" : ""}`}
                onClick={() => setSelected(coin.key)} aria-label={`${coin.base} 선택`}>
                <span className="nl-grid-title"><strong><CoinName coin={coin} /></strong><span>{amount(coin.cumulative.amount)}</span></span>
                <HourBars hours={coin.hours ?? []} compact />
                <small>{STATE_LABEL[coin.state]}</small>
              </button>)}
            {!filters.coins.some(coin => coin.hours) && <p className="nl-visual-empty">{data ? "표시할 상장 코인이 없습니다." : "자료 연결 중"}</p>}
          </div>
        </div>
      </aside>
    </main>
  );
}

function ExchangeCells({ coin, amount }: { coin: Coin; amount: (v: number | null | undefined) => string }) {
  return <>{EXCHANGES.map(([id]) => {
    const info = coin.exchanges[id];
    if (!info?.listed) return <td key={id} className="num nl-venue-cell" title="미상장"><span>—</span><small className="nl-kind">[미상장]</small></td>;
    return <td key={id} className="num nl-venue-cell" title={statusLabel(info.cumulative)}>
      <span>{formatShare(info.cumulative.share)}</span>
      <small title="거래금액">{amount(info.cumulative.amount)}</small>
      <small className={info.newly_listed ? `nl-kind is-new nl-ex-${id}` : "nl-kind"}>{kindLabel(coin, id)}</small>
    </td>;
  })}</>;
}

function CoinTable({ coins, total, sort, onSort, amount, now, selected, onSelect, loading }: {
  coins: Coin[]; total: number; sort: Sort; onSort: (field: SortField) => void; amount: (v: number | null | undefined) => string;
  now: number; selected: string | null; onSelect: (key: string) => void; loading: boolean;
}) {
  return <table className="nl-table nl-coin-table">
    <thead><tr>
      <SortHeading label="상장 거래소" field="exchange" sort={sort} onSort={onSort} />
      <SortHeading label="코인" field="name" sort={sort} onSort={onSort} />
      <SortHeading label="상장(첫 체결)" field="listed" sort={sort} onSort={onSort} />
      <th>경과</th>
      <SortHeading className="num" label="누적" field="cumulative" sort={sort} onSort={onSort} />
      <SortHeading className="num" label="최근 1시간" field="recent" sort={sort} onSort={onSort} />
      <SortHeading className="num" label="현재 시간" field="current" sort={sort} onSort={onSort} />
      <SortHeading className="num" label="직전 시간 대비" field="change" sort={sort} onSort={onSort} />
      <th className="breakdown">거래소 구성</th>
      {EXCHANGES.map(([id, name]) => <SortHeading key={id} className={`num nl-venue-cell nl-venue-name nl-ex-${id}`} label={name} field={`ex:${id}`} sort={sort} onSort={onSort} />)}
      <th>상태</th>
    </tr></thead>
    <tbody>
      {coins.map(coin => {
        const hour = currentHour(coin);
        return <tr key={coin.key} className={coin.key === selected ? "is-selected" : ""} onClick={() => onSelect(coin.key)}>
          <td className="nl-ex-cell"><ExchangeTag coin={coin} /></td>
          <th scope="row"><button type="button" className="nl-row-button" onClick={() => onSelect(coin.key)}>{coin.base}</button><small>{coin.korean_name}</small></th>
          <td>{coin.anchor_at ? formatKST(coin.anchor_at) : "대기"}</td>
          <td>{elapsedLabel(coin, now)}</td>
          <td className="num" title={statusLabel(coin.cumulative)}>{amount(coin.cumulative.amount)}<small>{formatShare(coin.cumulative.share)}</small></td>
          <td className="num" title={`${formatKSTTime(coin.recent_hour_from)}부터 · ${statusLabel(coin.recent_hour)}`}>{amount(coin.recent_hour.amount)}</td>
          <td className="num" title={statusLabel(hour?.total)}>{amount(hour?.total.amount)}<small>{hour ? `+${hour.index}시간` : ""}</small></td>
          <td className="num">{formatChange(lastChange(coin))}</td>
          <td className="breakdown"><TextureBar segments={cellSegments(coinExchangeCells(coin))} label={`${coin.base} 거래소 구성`} /></td>
          <ExchangeCells coin={coin} amount={amount} />
          <td>{stillLoading(coin)
            ? <span className="nl-chip nl-chip-loading">불러오는 중</span>
            : <><span className={`nl-chip nl-chip-${coin.state.toLowerCase()}`}>{stateLabel(coin, now)}</span>{coin.cumulative.status === "PARTIAL" && <small>일부 미확인</small>}</>}</td>
        </tr>;
      })}
      {!coins.length && <tr><td colSpan={11 + EXCHANGES.length} className="nl-empty">
        {loading ? "자료를 불러오는 중입니다." : total ? "필터에 맞는 코인이 없습니다." : "최근 상장한 코인이 없습니다. 새 상장은 감지되는 대로 표시됩니다."}
      </td></tr>}
    </tbody>
  </table>;
}

function HourTable({ coins, mode, amount, selected, onSelect, loading }: {
  coins: Coin[]; mode: "hour" | "cumulative"; amount: (v: number | null | undefined) => string;
  selected: string | null; onSelect: (key: string) => void; loading: boolean;
}) {
  const indexes = Array.from({ length: 24 }, (_, i) => i + 1);
  // One row per listed exchange, labelled [신규상장] or [기존], and a total
  // row when more than one exchange lists the coin.
  const rows = coins.flatMap(coin => {
    const listed = EXCHANGES.filter(([id]) => coin.exchanges[id]?.listed);
    const exchangeRows = listed.map(([id, name]) => ({ coin, id, name, total: false }));
    const totalRow = listed.length > 1 || !listed.length ? [{ coin, id: "total", name: "합계", total: true }] : [];
    return [...exchangeRows, ...totalRow];
  });
  return <table className="nl-table nl-hour-table">
    <thead><tr>
      <th className="nl-sticky-ex">거래소</th>
      <th className="nl-sticky">코인</th>
      {indexes.map(index => <th key={index} className="num">+{index}</th>)}
    </tr></thead>
    <tbody>
      {rows.map((row, i) => {
        const first = i === 0 || rows[i - 1].coin.key !== row.coin.key;
        return <tr key={`${row.coin.key}-${row.id}`} className={`${row.coin.key === selected ? "is-selected" : ""}${first ? " nl-group-start" : ""}${row.total ? " nl-total-row" : ""}`}
          onClick={() => onSelect(row.coin.key)}>
          <td className="nl-sticky-ex nl-ex-cell">
            {row.total ? "합계" : <><span className={`nl-ex-name nl-ex-${row.id}`}>{row.name}</span><small className={row.coin.exchanges[row.id]?.newly_listed ? "nl-kind is-new" : "nl-kind"}>{kindLabel(row.coin, row.id)}</small></>}
          </td>
          <th scope="row" className="nl-sticky">
            <button type="button" className="nl-row-button" onClick={() => onSelect(row.coin.key)}>{row.coin.base}</button>
            {first && <small>{row.coin.anchor_at ? formatKST(row.coin.anchor_at) : "대기"}</small>}
          </th>
          {indexes.map(index => {
            const hour = row.coin.hours?.[index - 1];
            const cell = row.total ? hour?.total : hour?.exchanges[row.id];
            const value = mode === "hour" ? cell?.amount : row.total ? hour?.cumulative : hour?.exchange_cumulative?.[row.id];
            const status = hour?.status ?? "WAITING";
            return <td key={index} className={`num nl-hour-cell is-${status.toLowerCase()}`} title={`+${index}시간 · ${statusLabel(cell)}${hour ? ` · ${formatKST(hour.from)}부터` : ""}${!row.total && cell?.share != null ? ` · 비중 ${formatShare(cell.share)}` : ""}`}>
              {status === "PENDING" || !hour ? "·" : amount(value)}
            </td>;
          })}
        </tr>;
      })}
      {!rows.length && <tr><td colSpan={26} className="nl-empty">{loading ? "자료를 불러오는 중입니다." : "표시할 코인이 없습니다."}</td></tr>}
    </tbody>
  </table>;
}

function NoticeTable({ notices, loading }: { notices: NoticeInfo[]; loading: boolean }) {
  return <table className="nl-table nl-notice-table">
    <thead><tr><th>게시</th><th>거래소</th><th>제목</th><th>코인</th><th>원화</th><th>추적</th></tr></thead>
    <tbody>
      {notices.map(notice => <tr key={`${notice.exchange}-${notice.url}-${notice.title}`}>
        <td>{formatKST(notice.published_at)}</td>
        <td>{EXCHANGE_NAME[notice.exchange] ?? notice.exchange}{notice.source === "WEBSOCKET" ? <small>웹소켓</small> : null}</td>
        <td className="nl-notice-title"><a href={notice.url} target="_blank" rel="noreferrer noopener">{notice.title}</a></td>
        <td>{(notice.symbols ?? []).join(", ") || "—"}</td>
        <td>{notice.krw ? "예" : "아니오"}</td>
        <td>{notice.coin ? STATE_LABEL[notice.coin_state ?? ""] ?? notice.coin_state : notice.krw ? "마켓 대기" : "—"}</td>
      </tr>)}
      {!notices.length && <tr><td colSpan={6} className="nl-empty">{loading ? "공지를 불러오는 중입니다." : "최근 상장 공지가 없습니다."}</td></tr>}
    </tbody>
  </table>;
}

function HourBars({ hours, compact = false }: { hours: Hour[]; compact?: boolean }) {
  const peak = Math.max(0, ...hours.map(hour => hour.total.amount ?? 0));
  return <span className={`nl-hour-bars${compact ? " is-compact" : ""}`} role="img" aria-label="+1~+24시간 거래금액 막대">
    {hours.map(hour => {
      const height = peak > 0 && hour.total.amount ? Math.max(3, (hour.total.amount / peak) * 100) : 0;
      return <span key={hour.index} className={`nl-hour-bar is-${hour.status.toLowerCase()}`} style={{ height: `${height}%` }}
        title={`+${hour.index}시간 ${formatKRW(hour.total.amount)} · ${statusLabel(hour.total)}`}>
        {EXCHANGES.map(([id], index) => {
          const share = hour.exchanges[id]?.share;
          return share ? <span key={id} className={`nl-texture nl-texture-${TEXTURES[index]} nl-ex-${id}`} style={{ height: `${share}%` }} /> : null;
        })}
      </span>;
    })}
  </span>;
}

function CoinDetail({ coin, amount, now }: { coin: Coin | null; amount: (v: number | null | undefined) => string; now: number }) {
  if (!coin) return <div className="nl-detail-card"><p className="nl-visual-empty">코인을 고르면 시간별 거래금액을 보여 줍니다.</p></div>;
  const hours = coin.hours ?? [];
  // Every listed exchange gets its own column: a simultaneous listing and a
  // listing next to an exchange that already had the coin alike.
  const listed = EXCHANGES.filter(([id]) => coin.exchanges[id]?.listed);
  return <div className="nl-detail-card">
    <div className="nl-visual-heading"><span><CoinName coin={coin} /> {coin.korean_name}</span><span>{stateLabel(coin, now)}</span></div>
    <p className="nl-source">첫 체결 {formatKST(coin.anchor_at)} · 경과 {elapsedLabel(coin, now)}</p>
    <div className="nl-summary-heading"><span>누적 (최대 24시간)</span><strong>{amount(coin.cumulative.amount)}</strong></div>
    <TextureBar segments={cellSegments(coinExchangeCells(coin))} label={`${coin.base} 거래소 구성`} />
    <ul className="nl-detail-exchanges">
      {EXCHANGES.map(([id, name]) => {
        const info = coin.exchanges[id];
        const kind = info?.listed && info.newly_listed ? `${kindLabel(coin, id)} ${formatKSTTime(info.first_trade_at)}` : kindLabel(coin, id);
        return <li key={id}>
          <span className={`nl-ex-name nl-ex-${id}`}>{name}</span>
          <span>{kind}</span>
          <span className="num">{info?.listed ? `${amount(info.cumulative.amount)} · ${formatShare(info.cumulative.share)}` : "—"}</span>
          {info?.last_error && <small className="is-alert">읽기 오류: {info.last_error}</small>}
        </li>;
      })}
    </ul>
    {hours.length > 0 && <HourBars hours={hours} />}
    {hours.length > 0 && <table className="nl-table nl-mini-table">
      <thead><tr>
        <th>시간</th>
        {listed.length > 1 && listed.map(([id, name]) => <th key={id} className={`num nl-venue-name nl-ex-${id}`}>{name}<small>{kindLabel(coin, id)}</small></th>)}
        <th className="num">{listed.length > 1 ? "합계" : `${listed[0]?.[1] ?? ""} 거래금액`}</th>
        <th className="num">대비</th>
      </tr></thead>
      <tbody>
        {hours.filter(hour => hour.status !== "PENDING").map(hour => <tr key={hour.index} className={`is-${hour.status.toLowerCase()}`}>
          <th scope="row">+{hour.index}<small>{formatKSTTime(hour.from)}</small></th>
          {listed.length > 1 && listed.map(([id]) => {
            const cell = hour.exchanges[id];
            return <td key={id} className="num" title={statusLabel(cell)}>{amount(cell?.amount)}<small>{formatShare(cell?.share)}</small></td>;
          })}
          <td className="num" title={statusLabel(hour.total)}>{amount(hour.total.amount)}<small>{hour.status === "FINAL" ? `누적 ${amount(hour.cumulative)}` : statusLabel(hour.total)}</small></td>
          <td className="num">{formatChange(hour.change_pct)}</td>
        </tr>)}
      </tbody>
    </table>}
    {hours.length > 0 && <p className="nl-source">거래소 칸 아래는 그 시간의 거래소 비중입니다.</p>}
    {(coin.notices ?? []).length > 0 && <ul className="nl-detail-notices">
      {(coin.notices ?? []).map(notice => <li key={notice.url + notice.title}>
        <a href={notice.url} target="_blank" rel="noreferrer noopener">{EXCHANGE_NAME[notice.exchange]} 공지: {notice.title}</a>
      </li>)}
    </ul>}
  </div>;
}
