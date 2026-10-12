# new_listing — 업비트·빗썸 신규 상장 추적기

업비트·빗썸 원화(KRW) 마켓에 새로 상장된 코인의 **상장 후 24시간 거래금액**을, 첫 체결 시각부터
1시간 단위로 보여 주는 윈도우 콘솔 프로그램입니다. 실행하면 이 PC 안에서만 열리는 화면
(`http://127.0.0.1:8770/`)이 브라우저에 뜹니다.

이 문서는 보안 검토용으로 프로그램의 기능, 외부 접속, 이 PC에서 하는 일, 파일 구성을 모두 적었습니다.
문서의 모든 항목은 이 저장소의 코드와 같습니다.

## 1. 보안 요약

| 항목 | 내용 |
| --- | --- |
| 하는 일 | 거래소의 **공개 시세·공지 자료를 읽기만** 합니다 |
| 하지 않는 일 | 로그인, 주문, 출금, 계좌 조회, 개인정보 수집 기능이 **없습니다** |
| 인증 정보 | API 키·비밀번호·토큰을 **받지도, 쓰지도 않습니다** |
| 외부 접속 | `api.upbit.com`, `api-manager.upbit.com`, `api.bithumb.com`, `feed-api.bithumb.com` 네 곳, 모두 HTTPS GET |
| 외부로 보내는 값 | 마켓 코드(예: `KRW-BTC`), 조회 개수, 조회 기준 시각뿐입니다. 쿠키·식별자·PC 정보는 보내지 않습니다 |
| 받는 접속 | `127.0.0.1:8770`(이 PC 안)만 엽니다. 다른 PC에서는 접속할 수 없습니다 |
| 파일 | 어떤 파일도 **만들거나 쓰지 않습니다.** 설정 파일·DB·로그 파일이 없습니다 |
| 권한 | 관리자 권한이 필요 없습니다. 서비스 등록·자동 실행·레지스트리 변경이 없습니다 |
| 실행하는 다른 프로그램 | 켤 때 기본 브라우저로 화면 주소를 엽니다(`-no-browser`로 끌 수 있음). 그 외에는 없습니다 |
| 업데이트·원격 측정 | 자동 업데이트, 사용 통계 전송이 없습니다 |
| 외부 라이브러리 | Go는 **표준 라이브러리만** 씁니다. 화면은 React와 Pretendard 글꼴을 빌드할 때 실행 파일에 넣습니다 |
| 종료하면 | 모든 자료가 메모리에만 있으므로 함께 사라집니다 |

## 2. 기능

| 기능 | 설명 |
| --- | --- |
| 신규 상장 감지 | 두 거래소의 공식 마켓 목록을 5초마다 확인합니다. 켤 때 처음 받은 목록이 기준이고, 그 뒤 새로 나타난 원화 마켓을 신규 상장으로 봅니다. 빈 응답이나 절반 이하로 줄어든 응답은 거래소 오류로 보고 무시합니다 |
| 켤 때 최근 상장 찾기 | 원화 마켓마다 일봉을 조회해 첫 거래일이 최근 7일 안이면 신규로 봅니다. 10분봉·1분봉으로 첫 체결 분을 찾습니다. 3일 넘게 거래가 없다가 다시 거래된 마켓은 재상장으로 표시합니다 |
| 거래금액 누적 | 거래소 공식 1분봉의 거래대금(`candle_acc_trade_price`)을 더합니다. 끝난 1분은 끝난 뒤 다시 읽어 **확정**하고, 진행 중인 1분은 약 1초마다 다시 읽어 **진행 중**으로 표시합니다 |
| 1시간 구간 | 첫 체결 분부터 60분씩, +1시간부터 +24시간까지 계산합니다 |
| 거래소 비교 | 같은 코인이 두 거래소에 24시간 안에 상장하면 하나로 묶습니다. 다른 거래소에 이미 있던 코인도 같은 시간 구간으로 함께 계산해 `[신규상장] / [기존]`으로 비교합니다 |
| 상장 공지 | 빗썸 공식 공지 API를 3초마다, 업비트 공지 목록을 5초마다 확인합니다. 상장 공지가 오면 마켓 목록을 바로 다시 확인합니다 |
| 빈 구간 처리 | 읽지 못한 1분은 0으로 채우지 않고 "일부 미확인"으로 표시한 뒤 다시 읽어 채웁니다. 거래가 없던 1분은 거래소가 1분봉을 만들지 않으므로 0원입니다 |
| 화면 | MarketGo 인덱스2 디자인(다크 모드 기본)입니다. 코인 보기, 시간별 보기(+1~+24), 상장 공지 보기, 선택한 코인의 거래소별 시간표가 있습니다 |

## 3. 외부 접속 (데이터 수집)

모든 요청은 HTTPS **GET**이며 아래 주소 외에는 접속하지 않습니다. 요청 헤더는 `Accept: application/json`과
`User-Agent: listing-tracker/1`뿐이고, 쿠키를 저장하거나 보내지 않습니다.

| 주소 | 언제 | 보내는 값 | 받는 자료 |
| --- | --- | --- | --- |
| `https://api.upbit.com/v1/market/all?is_details=true` | 5초마다, 상장 공지가 오면 즉시 한 번 더 | 없음 | 업비트 마켓 목록 |
| `https://api.bithumb.com/v1/market/all?isDetails=true` | 5초마다, 상장 공지가 오면 즉시 한 번 더 | 없음 | 빗썸 마켓 목록 |
| `https://api.upbit.com/v1/candles/days` | 켤 때 원화 마켓마다 한 번 | `market`, `count=10` | 일봉 |
| `https://api.bithumb.com/v1/candles/days` | 켤 때 원화 마켓마다 한 번 | `market`, `count=10` | 일봉 |
| `https://api.upbit.com/v1/candles/minutes/10`, `…/minutes/1` | 켤 때 최근 상장 후보마다 한 번씩 | `market`, `count`, `to` | 10분봉·1분봉 |
| `https://api.bithumb.com/v1/candles/minutes/10`, `…/minutes/1` | 위와 같음 | `market`, `count`, `to` | 10분봉·1분봉 |
| `https://api.upbit.com/v1/candles/minutes/1` | 추적 중인 마켓마다 약 1초에 한 번, 빈 구간은 200개씩 | `market`, `count`, `to` | 1분봉 |
| `https://api.bithumb.com/v1/candles/minutes/1` | 위와 같음 | `market`, `count`, `to` | 1분봉 |
| `https://api.bithumb.com/v1/notices?count=20` | 3초마다 | `count=20` | 빗썸 공지 목록 (`https://feed-api.bithumb.com`으로 리다이렉트됨) |
| `https://api-manager.upbit.com/api/v1/announcements?os=web&page=1&per_page=20&category=trade` | 5초마다 | 고정 값 | 업비트 거래 공지 목록 (공식 Open API가 아닌 업비트 웹사이트용 주소) |

- **리다이렉트**: 최대 3번까지, `upbit.com`·`bithumb.com` 도메인의 https 주소로만 따라갑니다. 그 밖의 주소는 거부합니다.
- **응답 처리**: 응답은 8MB까지만 읽습니다. JSON 형식이 아니거나 값이 음수면 버립니다.
- **시간 제한**: 요청마다 5~8초, 여러 페이지를 읽는 작업은 60초입니다.
- **PC 시계 보정**: 거래소 응답의 `Date` 헤더로 PC 시계를 보정해, 1분 경계를 거래소 시각에 맞춥니다.

### 요청 한도

| 거래소 | 공식 한도 (IP 기준) | 이 프로그램의 상한 |
| --- | --- | --- |
| 업비트 | 그룹(market, candle 등)별 초당 10회 | 그룹별 초당 8회. 진행 중 1분 갱신은 전체 초당 4회 안에서 나눔. 공지 목록은 3초에 한 번 이하 |
| 빗썸 | 분류별 초당 150회, 공지 IP당 초당 1회 | 분류별 초당 50회. 진행 중 1분 갱신은 전체 초당 10회 안. 공지는 1.1초에 한 번 이하 |

- **429/418 응답**: 거래소가 429(요청 초과)나 418(일시 차단)을 보내면 해당 그룹을 1초부터 최대 60초까지 기다린 뒤 다시 요청합니다. 418이면 바로 60초를 기다립니다.
- **업비트 남은 횟수 헤더**: `Remaining-Req`의 초당 남은 횟수가 1 이하이면 다음 초까지 기다립니다.
- **같은 PC의 다른 업비트 프로그램**: 업비트 한도를 나눠 쓰므로 `-upbit-rate`로 상한을 낮출 수 있습니다.

## 4. 이 PC에서 하는 일

| 항목 | 내용 |
| --- | --- |
| 여는 포트 | `127.0.0.1:8770` 하나 (`-addr`로 바꿀 수 있음). 이 PC 안에서만 접속됩니다 |
| 화면 주소의 기능 | `GET /api/snapshot`(자료 JSON), `GET /api/stream`(1초마다 자료를 보내는 이벤트 스트림, 동시 64개까지), `GET /`(화면 파일). **읽기만 있고** 쓰기·변경 요청은 없습니다. GET 외의 요청은 405로 거부합니다 |
| 화면 주소 보호 | 이 PC 주소로 열린 경우 `Host`가 `127.0.0.1`·`localhost`·`[::1]`과 같은 포트가 아니면 403으로 거부합니다(다른 웹사이트의 DNS 리바인딩 차단). `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, 자료 응답은 `Cache-Control: no-store` |
| 파일 읽기 | 없습니다. 화면 파일은 실행 파일 안에 들어 있습니다. 개발용 `-web 폴더` 옵션을 줄 때만 그 폴더의 화면 파일을 읽습니다 |
| 파일 쓰기 | 없습니다 |
| 브라우저 저장 | 화면 밝기 선택 하나만 브라우저 저장소(`localStorage`의 `listing-tracker-theme`)에 저장합니다 |
| 화면이 접속하는 곳 | 같은 주소의 `/api/stream`, `/api/snapshot`뿐입니다. 글꼴·스크립트를 외부(CDN)에서 받지 않습니다. 공지 제목을 누르면 거래소 공지 페이지가 새 탭으로 열립니다(`rel="noreferrer noopener"`) |
| 실행하는 프로그램 | 켤 때 화면 주소를 기본 브라우저로 엽니다. 윈도우 `rundll32 url.dll,FileProtocolHandler http://127.0.0.1:8770/`, macOS `open`, 리눅스 `xdg-open`. `-no-browser`로 끌 수 있습니다 |
| 윈도우 API | `kernel32.dll`의 `SetConsoleOutputCP(65001)`, `SetConsoleCP(65001)`. 콘솔 창의 한글이 깨지지 않게 UTF-8로 바꾸는 것뿐입니다 |
| 콘솔 출력 | 진행 상황(상장 감지, 최근 상장, 오류)을 콘솔 창에만 출력합니다 |

## 5. 실제로 돌아가는 내용

### 켤 때 순서

1. `main.go` `main()` — 윈도우면 콘솔을 UTF-8로 바꿉니다(`console_windows.go` `useUTF8Console()`).
2. `main.go` `run()` — 실행 옵션을 읽고 부품을 만듭니다.
   - 외부 요청 출입구 `restclient.New()`
   - 캔들 조회 `candles.NewClient()`
   - 추적 저장소(메모리) `tracker.New()`
   - 마켓 목록 감시 `registry.New()`
   - 최근 상장 찾기 `discovery.New()`
   - 공지 감시 `notices.NewWatcher()`
3. `127.0.0.1:8770`을 엽니다. 이 PC 주소면 `httpapi` `LoopbackOnly()`로 `Host` 확인을 켭니다.
4. 콘솔에 시작 요약을 출력합니다(`console.go` `banner()`).
5. 아래 작업들을 동시에 시작합니다.
6. 기본 브라우저로 화면 주소를 엽니다(`main.go` `openBrowser()`, `-no-browser`면 생략).
7. Ctrl+C(종료 신호)를 받으면 화면 서버를 닫고 모든 작업을 멈춘 뒤 끝냅니다.

### 켜져 있는 동안 돌아가는 작업

| 작업 | 개수 | 주기 | 파일 · 함수 | 하는 일 |
| --- | --- | --- | --- | --- |
| 마켓 목록 감시 | 거래소별 1개 | 5초 | `registry.go` `Run()` → `PollOnce()` → `Diff()` | 마켓 목록을 받아 이전 목록과 비교합니다. 새 원화 마켓이면 `tracker.AddListing()`에 넘깁니다 |
| 최근 상장 찾기 | 거래소별 1개 | 켤 때 한 번 | `main.go` `scan()` → `discovery.go` `Scan()` → `check()` | 첫 마켓 목록을 받은 뒤 원화 마켓마다 일봉을 보고, 최근 7일 안에 처음 거래된 마켓의 첫 체결 분을 찾아 `tracker.AddListing()`에 넘깁니다 |
| 1분봉 수집 | 거래소별 1개 | 계속 (추적 중인 마켓당 약 1초, 첫 체결 대기 마켓은 3초) | `fetch.go` `RunFetcher()` → `nextJob()` → `planLocked()` → `execute()` → `apply()` | 마켓마다 필요한 일을 고릅니다: 첫 체결 찾기, 빈 구간 채우기(`candles.go` `MinuteRange()`), 최신 몇 분 다시 읽기(`candles.go` `Minutes()`). 끝난 1분은 확정하고 진행 중인 1분은 갱신합니다 |
| 공지 감시 | 거래소별 1개 | 빗썸 3초, 업비트 5초 | `watcher.go` `Run()` → `poll()` → `notices.go` `ParseBithumb()` / `ParseUpbitList()` → `Classify()` | 공지 목록을 읽어 상장 공지를 골라 `tracker.AddNotice()`에 넣습니다. 새 상장 공지면 그 거래소의 마켓 목록을 바로 다시 확인합니다(같은 거래소는 2초에 한 번까지) |
| 상태 정리 | 1개 | 1초 | `tracker.go` `Maintain()` | 7일이 지난 추적을 지우고, 같은 코인을 가진 다른 거래소 마켓을 붙이고, `-watch` 코인을 시작합니다 |
| 화면 서버 | 1개 | 요청이 올 때 | `server.go` `Handler()` → `snapshot.go` `Snapshot()` | 메모리의 자료로 +1~+24시간, 누적, 최근 1시간, 비중을 계산해 JSON으로 보냅니다. `/api/stream`은 1초마다 보냅니다 |

모든 외부 요청은 `restclient.go` `Get()` 하나를 거칩니다. 거래소별 요청 간격, 429/418 대기, 응답 8MB 제한, 리다이렉트 제한이 여기서 적용됩니다.

### 자료 흐름

```text
업비트·빗썸 마켓 목록 ──▶ registry (신규 상장 판별) ──┐
켤 때 일봉·10분봉·1분봉 ──▶ discovery (최근 7일 상장) ─┼──▶ tracker (메모리) ──▶ snapshot ──▶ 127.0.0.1:8770 화면
업비트·빗썸 공지 목록 ──▶ notices (상장 공지 판별) ────┘          ▲
                                       1분봉 ──▶ fetch (확정·진행 중) ┘

외부 요청은 모두 restclient.Get() → 업비트·빗썸 HTTPS GET
```

### 실행 파일에 들어가는 Go 파일

모든 Go 코드는 표준 라이브러리만 사용합니다.

| 파일 | 기능 |
| --- | --- |
| `cmd/listing-tracker/main.go` | 시작·종료와 부품 연결. `run()` 부품 생성과 작업 시작, `scan()` 켤 때 최근 상장 찾기, `openBrowser()` 화면 열기 |
| `cmd/listing-tracker/console.go` | 콘솔 출력. `banner()` 시작 요약, `line()` `시각  거래소  내용` 한 줄, `usage()` `-h` 한글 안내, `consoleHandler` 경고·오류 형식 |
| `cmd/listing-tracker/console_windows.go` | 윈도우 전용. `useUTF8Console()`이 `kernel32.dll` `SetConsoleOutputCP`·`SetConsoleCP`(65001)로 콘솔을 UTF-8로 바꿉니다 |
| `cmd/listing-tracker/console_other.go` | 윈도우가 아닌 OS용 빈 함수 |
| `internal/market/market.go` | 거래소·마켓 정의와 마켓 목록 주소. `ParseMarketList()`가 원화 마켓만 꺼내고, JSON 배열이 아니면 거부합니다 |
| `internal/registry/registry.go` | 신규 상장 감지. `Diff()`가 첫 목록을 기준으로 삼아 신규·재상장을 찾습니다. 3번 연속 빠져야 목록에서 지우고, 빈 응답이나 절반 이하로 줄어든 응답은 거부합니다. `ActiveMarketsForBase()`는 같은 코인을 가진 거래소를 알려 줍니다. 아무것도 저장하지 않습니다 |
| `internal/discovery/discovery.go` | 켤 때 최근 상장 찾기. `check()`가 일봉 → 10분봉 → 1분봉으로 첫 체결 분을 찾고, `firstDayInWindow()`가 첫 거래일(또는 3일 넘게 쉬고 다시 거래된 날)이 7일 안인지 판단합니다. `Progress()`는 진행률입니다 |
| `internal/candles/candles.go` | 캔들 조회. `Minutes()`·`Days()` 요청, `Parse()` 응답 해석(음수·잘못된 형식 거부), `MinuteRange()` 200개씩 거꾸로 넘기며 구간 모으기, `formatTo()` 거래소별 기준 시각 형식(업비트 UTC, 빗썸 한국시각) |
| `internal/tracker/tracker.go` | 추적 대상 관리(메모리). `AddListing()`은 코인별 추적을 만들거나, 24시간 안의 같은 코인과 묶습니다. 기준 시각은 신규 마켓 중 가장 이른 첫 체결입니다. `Maintain()`은 1초마다 정리하고, `AddManual()`은 `-watch` 코인, `AddNotice()`는 공지를 받습니다 |
| `internal/tracker/fetch.go` | 1분봉 수집 순서. `nextJob()`이 가장 오래 기다린 마켓을 고르고, `planLocked()`가 할 일을 정하고, `apply()`가 결과를 넣습니다. 실패하면 2초부터 최대 60초까지 기다립니다. `addFinal()`은 끝난 1분을 확정 구간에 넣고, 확정 구간은 항상 이어져 있습니다 |
| `internal/tracker/snapshot.go` | 화면 자료 계산. `Snapshot()`이 코인별 +1~+24시간, 누적, 최근 1시간, 거래소별 비중, 공지 연결을 만듭니다. `minute()`은 1분을 확정·진행 중·미확인으로 구분하고, `span()`·`combine()`은 구간 합계와 상태를 정합니다 |
| `internal/notices/notices.go` | 공지 해석. `ParseBithumb()`·`ParseUpbitList()`가 목록을 읽습니다. `Classify()`는 제목으로 상장 공지를 판별하고(예: "신규 거래지원", "마켓 추가"; "종료·유의·이벤트" 등은 제외), 괄호 안 코인 기호와 원화 여부를 꺼냅니다 |
| `internal/notices/watcher.go` | 공지 감시. `Run()`이 빗썸 3초·업비트 5초마다 읽고, `Statuses()`가 마지막 성공·오류를 화면에 줍니다 |
| `internal/restclient/client.go` | 외부 요청 출입구. `Get()` 거래소·그룹별 간격과 429/418 대기, `NewHTTPClient()` 리다이렉트를 거래소 도메인 https로만 허용, `Now()` `Date` 헤더로 보정한 시각, `SetUpbitRate()` `-upbit-rate` 옵션 |
| `internal/httpapi/server.go` | 이 PC 안의 화면 서버. `Handler()`가 `/api/snapshot`·`/api/stream`·화면 파일을 GET으로만 제공합니다. `checkHost()`는 이 PC 이름이 아닌 `Host`를 거부하고, `stream()`은 동시 64개까지 1초마다 보냅니다. 자료는 0.5초 동안 재사용합니다 |
| `internal/webui/webui.go` | 빌드한 화면 파일을 실행 파일 안에 넣습니다(`go:embed`) |

## 6. 화면 파일 (`web/`)

| 파일 | 기능 |
| --- | --- |
| `src/App.tsx` | 3단 화면: 왼쪽 요약·필터, 가운데 코인·시간별·공지 표, 오른쪽 선택 코인의 거래소별 시간표와 카드 |
| `src/api.ts` | 자료 형식과 수신. `useSnapshot()`이 `/api/stream`을 받고, 끊기면 2초마다 `/api/snapshot`을 읽습니다 |
| `src/format.ts` | 금액(억)·비중·시각 표기 |
| `src/FilterButton.tsx` | 인덱스2 필터 버튼 |
| `src/theme.ts`, `src/theme.css` | 다크(기본)·라이트 색. 선택은 브라우저 저장소에 저장 |
| `src/listing.css` | MarketGo 인덱스2(`market-index2.css`)에서 가져온 화면 모양 |
| `package.json`, `package-lock.json` | 정확한 버전으로 고정한 화면 의존성과 각 패키지의 무결성 해시 |

## 7. 의존성

| 구분 | 패키지 | 버전 | 라이선스 | 쓰임 |
| --- | --- | --- | --- | --- |
| Go | 표준 라이브러리만 | Go 1.26 이상 | BSD-3-Clause | 프로그램 전체 |
| 화면 (실행 파일에 포함) | react, react-dom | 19.2.7 | MIT | 화면 |
| 화면 (실행 파일에 포함) | pretendard | 1.3.9 | OFL-1.1 | 한글 글꼴 |
| 빌드 도구 (포함 안 됨) | vite | 8.1.3 | MIT | 화면 묶기 |
| 빌드 도구 (포함 안 됨) | typescript | 6.0.3 | Apache-2.0 | 타입 검사 |
| 빌드 도구 (포함 안 됨) | @vitejs/plugin-react, @types/react, @types/react-dom | 6.0.3, 19.2.17, 19.2.3 | MIT | 빌드 보조 |

묶은 화면 파일 안에 `https://react.dev/errors/` 글자가 들어 있습니다. React 오류 안내 문구일 뿐이고, 화면이 이 주소에 접속하지는 않습니다.

## 8. 빌드와 검증

필요한 것: Go 1.26 이상, Node.js와 npm. 이 저장소의 결과물은 Go 1.27.0, Node 26.8.1로 만들었습니다.

```sh
VERSION=1.0.0 ./scripts/build.sh
```

`scripts/build.sh`가 하는 일:

1. `web/`에서 `npm ci`(잠금 파일 그대로 설치), `npm run build`(타입 검사와 화면 묶기)
2. 묶은 화면을 `internal/webui/dist/`로 복사
3. `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$VERSION"`
   → `dist/listing-tracker.exe`, 같은 방식으로 현재 OS용 `dist/listing-tracker`

같은 커밋을 같은 Go·Node 버전과 같은 `VERSION`으로 두 번 빌드하면 같은 SHA-256이 나오는 것을 이 PC에서 확인했습니다.
검토한 원본과 배포 파일이 같은지 이렇게 확인할 수 있습니다.

```sh
VERSION=1.0.0 ./scripts/build.sh && shasum -a 256 dist/listing-tracker.exe
```

`dist/`(실행 파일)와 화면 빌드 결과는 저장소에 올리지 않습니다.

## 9. 사용법

`listing-tracker.exe`를 실행하면 콘솔 창에 진행 상황이 나오고 브라우저가 열립니다. 끌 때는 창을 닫거나 `Ctrl+C`를 누릅니다.

| 옵션 | 기본 | 설명 |
| --- | --- | --- |
| `-watch BTC,ETH` | 없음 | 상장과 관계없이 지금부터 직접 추적할 코인 |
| `-no-browser` | 끔 | 켤 때 브라우저를 열지 않음 |
| `-addr` | `127.0.0.1:8770` | 화면 주소 |
| `-lookback` | `168h` | 켤 때 찾을 상장 기간 (7일) |
| `-registry-interval` | `5s` | 마켓 목록 확인 간격 |
| `-upbit-rate` | 8 | 업비트 그룹별 초당 요청 상한 (최대 8) |
| `-verbose` | 끔 | 자세한 기록 출력 |
| `-web` | 없음 | 화면 파일 폴더 (개발용) |

`-addr`을 `0.0.0.0:8770`처럼 바꾸면 다른 PC에서도 접속할 수 있게 됩니다. 이때는 `Host` 확인이 꺼지므로 기본값 그대로 쓰기를 권합니다.

## 10. 알려진 제한

- 업비트 공지 목록 주소(`api-manager.upbit.com`)는 공식 Open API가 아니어서 바뀌거나 막힐 수 있습니다. 막혀도 상장 감지는 마켓 목록으로 계속됩니다.
- 켤 때 찾기는 최근 7일만 봅니다. 그보다 오래된 상장은 `-watch`로 직접 추가해야 하며, 이때는 켠 시각부터 셉니다.
- 거래소가 1분봉을 늦게 고치면, 이미 확정한 1분에는 반영되지 않습니다.
- 서명되지 않은 실행 파일이라 윈도우가 처음 실행할 때 경고를 띄울 수 있습니다.
