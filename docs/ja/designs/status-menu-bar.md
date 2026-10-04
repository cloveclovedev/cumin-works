# モニターファイルとメニューバーのアプリの設計

- 状態: Draft
- 要件: 要求Issue [#392](https://github.com/cloveclovedev/cumin-works/issues/392) (cuminの状態をmacOSのメニューバーに表示する)。要件文書への反映は、[#500](https://github.com/cloveclovedev/cumin-works/issues/500) が [cumin本体の要件](../requirements/cumin-core.md) の「状態の持ち方」に行う
- 事実の出どころ: 公式ドキュメントはページの名前で示す。SF Symbolsの記号が使える版は、macOSに入っている一覧 (CoreGlyphs の `name_availability.plist`) で確かめ、「一覧」と書く。それ以外は、この文書で決めたことである。

`cumin run` が書くモニターファイル (`monitor.json`) の中身と、それを読んでmacOSのメニューバーに表示するアプリの作りを決める。

## 範囲

扱うこと:

- モニターファイルの場所、書く時点、全てのフィールド。ファイルは、cuminとアプリの間のただ1つの約束である。
- アプリの技術、置き場所、ファイルの読み方、表示、Ownerへの知らせ方、設定。

扱わないこと:

- ファイルに載せる事実をcuminがどう得るか。[定期確認の設計](poll.md) と [利用枠の設計](quota.md) にある。
- アプリのビルドと起動の手順、LaunchAgentのplist。`docs/ja/guides/` に書く ([#507](https://github.com/cloveclovedev/cumin-works/issues/507))。
- GitHubをアプリから読むこと、macOS以外のOS。#392 が範囲の外としている。

## 設計

- 採らなかった案: Goのアプリと、third-partyのメニューバーのライブラリ。cgoを使う新しい依存が要る (Ownerの判断、#392)。
- 採らなかった案: SwiftBar (third-partyのメニューバーのホスト) のスクリプト。Ownerがthird-partyのアプリを入れることになる (Ownerの判断、#392)。
- 採らなかった案: アプリがGitHubを読む。アプリにtokenが要り、cuminが既に読んだ事実をもう一度読むことになる (Ownerの判断、#392)。

### 2つの部品と約束

![モニターファイルとメニューバーのアプリ](status-menu-bar.svg)

図の元ファイル: [status-menu-bar.puml](status-menu-bar.puml)

- 部品は2つで、対等である。片方がなくても、もう片方は動く。書く側は `cumin run`、読む側はアプリである。2つをつなぐのはモニターファイルだけで、互いのコードも設定も読まない。
- `cumin run` はアプリを知らない。アプリがなくても同じファイルを書く。同じファイルを読めば、別の表示 (CLI、ウィジェット) も作れる。
- アプリは、cuminの設定 (`config.toml`)、手元の状態 (`state.json`)、ログ、Keychain、GitHubを読まない。ネットワークを使わない。
- ファイルには、token、鍵、webhookのアドレス、使用率の数値を入れない。入れるのは、Issueの番号とURL、role、依頼の種類、時刻、定期確認のエラーの文章、利用枠の状態の名前だけである。

### モニターファイル

- 場所は `~/.local/state/cumin/monitor.json` である。cuminを外から見る道具のためのファイルであり、cumin自身の状態ではないので、`state.json` と紛れない名前にする。[cumin本体の設計メモ](cumin-core.md) の「Hostに置くファイル」の決まり (JSON、先頭に `version`、同じディレクトリの一時ファイルに書いてから rename、書くプロセスは1つだけ) に従う。権限は `state.json` と同じ (ファイルは0600) にする。
- 書くのは `cumin run` だけである。cuminは、このファイルを読まず、ここから何も決めない。起動時にも読まず、次に書くときに上書きする。失っても、次に書くまで表示が古くなるだけである。
- 書く時点は2つである。定期確認の1回り (`poll_interval` ごとの、全ての対象リポジトリの確認) の終わりと、Agentの実行の終わりである。どのリポジトリも飛ばした回りでも書く。`last_poll.at` が `poll_interval` ごとに進むので、アプリは1つの決まった上限で、cuminが止まったことに気付ける。
- 採らなかった案: リポジトリを確かめた回りだけ書く。作業中のIssueがないと、書く間隔が `idle_poll_interval` まで延び、止まったことに気付くのが遅れる。
- 採らなかった案 (前の決定): 実行中の一覧をファイルに書かない。手元のファイルが1つ増えるためだった。Ownerは2026-10-03に、表示のために書くと決めた (#392)。
- 採らなかった案: 止まるときに「止まった」と書く。異常終了では書けないので、古いファイルの判定はどちらにしても要る。
- 中身は、定期確認が既に持っている事実から、純粋関数で作る。このファイルのために、GitHubへの問い合わせも、使用率を読む最小の実行も増やさない。関数は `internal/workflow` に、書き込みは `internal/core/state` に置く。
- 時刻は、RFC 3339のUTCの文字列にする (`state.json` と同じ)。
- `version` を上げるのは、読む側が壊れる変更のときだけにする。フィールドを足すだけなら上げない。アプリは、知らないフィールドを読み飛ばし、自分が知る版より新しい `version` のファイルは表示しない。

フィールドは次の通りである。配列は、空のときも `[]` として必ず書く。

| フィールド | 型 | 意味 |
|---|---|---|
| `version` | 整数 | 形式の版。今は `1` |
| `last_poll.at` | 時刻 | 最後の定期確認の1回りが終わった時刻。アプリは、これで古いファイルを判定する |
| `last_poll.errors` | 配列 | 最後の確認が失敗したままのリポジトリ。全て成功していれば空。飛ばしたリポジトリは、前の結果を保つ |
| `last_poll.errors[].repository` | 文字列 | `<owner>/<repo>` |
| `last_poll.errors[].message` | 文字列 | 失敗の理由を1行にしたもの。定期確認が続けて失敗したときの通知 ([定期確認の設計](poll.md)) に載せる文章と同じである |
| `stop_requested` | 真偽 | 実行を待ってから止まる途中か ([cumin本体の設計メモ](cumin-core.md) の「実行を待ってから止める」) |
| `quota.state` | 文字列 | `open` (新しい着手を進める)、`stopped` (上限に達して止めている、Q1)、`unread` (使用率を読み取れず止めている、Q1) |
| `quota.stopped_windows` | 配列 | 上限に達した枠の名前。`5h` と `weekly`。`stopped` のときだけ要素を持つ |
| `quota.next_try_at` | 時刻 | 次に着手を試す時刻 (Q3)。ないときは、キーを書かない |
| `agents` | 配列 | `cumin run` が実行中として持つAgentの実行。1つの実行が1つの要素 |
| `agents[].repository` | 文字列 | `<owner>/<repo>` |
| `agents[].issue` | 整数 | Issueの番号 |
| `agents[].role` | 文字列 | `planner`、`implementer`、`reviewer` |
| `agents[].request` | 文字列 | 依頼の種類。roleの指示ファイルが使う名前 (`plan`、`acceptance check`、`implement`、`continue`、`check fix`、`conflict resolution`、`review`、`review fix`、`owner review fix`、`explain the cause`) |
| `agents[].url` | 文字列 | IssueのURL |
| `waiting` | 配列 | Ownerの対応を待つ、開いているIssue。リポジトリごとに、最後に読めたスナップショットから作る |
| `waiting[].repository` | 文字列 | `<owner>/<repo>` |
| `waiting[].issue` | 整数 | Issueの番号 |
| `waiting[].kind` | 文字列 | `owner-review` (`cumin/status/awaiting-owner-review`、Ownerが承認する)、`owner-decision` (`cumin/status/awaiting-owner-decision`、Ownerが答える) |
| `waiting[].url` | 文字列 | IssueのURL |

例:

```json
{
  "version": 1,
  "last_poll": {
    "at": "2026-10-04T07:00:05Z",
    "errors": [
      {"repository": "example/app", "message": "read the snapshot: GitHub returned 502"}
    ]
  },
  "stop_requested": false,
  "quota": {
    "state": "stopped",
    "stopped_windows": ["5h"],
    "next_try_at": "2026-10-04T09:00:00Z"
  },
  "agents": [
    {"repository": "example/tool", "issue": 12, "role": "implementer", "request": "implement", "url": "https://github.com/example/tool/issues/12"}
  ],
  "waiting": [
    {"repository": "example/tool", "issue": 9, "kind": "owner-review", "url": "https://github.com/example/tool/issues/9"},
    {"repository": "example/app", "issue": 31, "kind": "owner-decision", "url": "https://github.com/example/app/issues/31"}
  ]
}
```

- `waiting[].kind` は、ラベルだけから決める。判断の依頼と、止まったIssueは、どちらも `owner-decision` で、分けない。分けるにはコメントを読むことになる。
- 利用枠は、状態の名前と、枠の名前と、次に試す時刻だけを載せる。使用率、上限、リセット時刻は載せない。受け入れた不利益: weekly枠の次に試す時刻は使用率から計算するので、設定を知る人は使用率を逆算できる。ファイルはHostのユーザだけが読める。
- 採らなかった案: Issueの題名を載せる。表示は読みやすくなるが、実行中の一覧は題名を持たず、載せるものが増える。

### アプリの構成

- Swiftのpackageを `tools/status-bar/` に置き、`swift build` で作る。third-partyの依存は持たない (Ownerの判断、#392)。Goのモジュールとは別で、`go build ./...` はこのディレクトリを読まない。
- 実行ターゲット1つと、テストのターゲット1つにする。表示はAppKitの `NSStatusItem` で作り、Dockには出さない。
- 採らなかった案: SwiftUIの `MenuBarExtra`。点滅のたびに1枚の画像を描き直して差し替えるので、画像を直接渡せる `NSStatusItem` のほうが単純である。
- 判定は純粋な型にまとめる。入力は、ファイルのバイト列、設定、今の時刻、前に見た項目である。出力は、メニューバーの区画、メニューの行、鳴らす音である。AppKitを使うのは、入口のファイルだけにする。
- テストは `swift test` (XCTest) で、純粋な型だけを試す。時刻は引数で渡すので、どのマシンでも同じ結果になる。Goの受け入れテストのgolden fileを、Swiftのテストも読む。1つのファイルが、書く側と読む側の両方を確かめる。
- CIのmacOSのjobは、Ownerの作業である ([#506](https://github.com/cloveclovedev/cumin-works/issues/506))。それまでは、ImplementerとReviewerがHostで `swift test` を実行する。
- 起動は、ログイン時のLaunchAgentで行う。cuminのLaunchAgentとは別のものである。

### ファイルの読み方と表示

- アプリは、5秒ごとのタイマーで、モニターファイルと設定を読み直す。
- 採らなかった案: ディレクトリの監視。同じディレクトリに `state.json` とログがあり、ログの1行ごとに通知が来る。
- メニューバーには、区画を3つまで出す。区画は、SF Symbolsの記号1つと数字1つである。全ての区画を1枚のテンプレート画像に描くので、メニューバーの明暗に合わせて単色になる。

| 区画 | 数 | ファイルから | 記号 |
|---|---|---|---|
| 実行中 | 続いているAgentの実行 | `agents` の長さ | `play.fill` |
| 承認を待つ | Ownerのレビューを待つIssue | `kind` が `owner-review` の `waiting` | `hand.raised.fill` |
| 答えを待つ | Ownerの判断を待つIssue | `kind` が `owner-decision` の `waiting` | `questionmark.circle.fill` |

- 数は、Hostの全てのリポジトリの合計である。数が0の区画は描かない。区画が1つも残らないときは、中立の記号 (`circle`) を1つ描き、項目が消えないようにする。
- 実行中の区画は、roleで分けない。roleと依頼の種類は、メニューの行に出す。
- 4つの記号は、どれもSF Symbolsの最初の版 (2019) からある (一覧)。記号から画像を作る `NSImage(systemSymbolName:accessibilityDescription:)` は macOS 11.0 からなので (公式: NSImage の同名のページ)、アプリが対応するどの版でも使える。
- メニューを開くと、`agents` と `waiting` の1行ずつが並ぶ。行を選ぶと、`url` を既定のブラウザで開く。その下に、利用枠の状態、止める予約、定期確認のエラー、最後の定期確認の時刻、Quitを出す。
- 古いファイルの判定: 今の時刻から `last_poll.at` を引いた値が上限を超えたら、古いとする。上限の初期値は180秒で、設定で変えられる。`poll_interval` の初期値 (60秒) の3回分であり、1回りが遅れても古いとしない。`poll_interval` を長くしたHostでは、Ownerが上限も長くする。
- 採らなかった案: 上限をファイルに載せる (cuminが `poll_interval` から計算する)。Ownerは決まった上限とした (#392)。約束のフィールドも1つ増える。
- 古いファイル、ないファイル、読めないファイル、新しすぎる `version` のファイルでは、区画を出さず、中立の記号を薄くして1つ出す。メニューには理由を1行出す。古い数字を、今の状態のように見せないためである。

### Ownerへの知らせ方

- `waiting` に新しい項目が現れたら、音を1回鳴らし、その項目の区画を点滅させる。対象は、待つ2つの区画 (承認を待つ、答えを待つ) で、それぞれ自分の新しい項目で鳴り、点滅する。実行中の区画は、鳴らず、点滅しない。項目は、`repository`、`issue`、`kind` の組で見分ける。
- アプリの起動のあと、最初に読んだ項目では鳴らさない。アプリを起動し直すたびに、既に知っている項目で鳴らさないためである。
- 見た項目は、アプリのメモリにだけ持つ。ファイルが古い間は、新しい項目を数えない。
- 採らなかった案: Notification Centerの通知。#392 の計画が範囲の外とした。app bundleが要るかは未確認である。
- Ownerへの通知 (Discord) は、今までどおり `cumin run` が出す。アプリの音は、それを置き換えない。

### アプリの設定

- 場所は `~/.config/cumin/status-bar.json` で、Ownerが編集する。全てのキーは任意で、ないキーは初期値になる。ファイルがない、または読めないときは、全て初期値にする。
- TOMLにしない。標準ライブラリにTOMLの読み取りがなく、third-partyの依存を持たないためである。cuminの設定の一覧 ([設定の一覧](../development/configuration.md)) には載せず、ガイド (#507) に書く。

| キー | 意味 | 初期値 |
|---|---|---|
| `stale_after_sec` | 古いファイルとする上限 (秒) | `180` |
| `sound_owner_review` | 承認を待つ新しい項目で鳴らす、システムの音の名前。`""` は鳴らさない | `"Glass"` |
| `sound_owner_decision` | 答えを待つ新しい項目で鳴らす、システムの音の名前。`""` は鳴らさない | `"Tink"` |
| `blink` | 待つ2つの区画を点滅させるか | `true` |

## まだ決めていないこと

| 決める、または確かめること | どこで |
|---|---|
| 点滅をいつ止めるか (メニューを開いたとき、または決まった時間のあと) | [#505](https://github.com/cloveclovedev/cumin-works/issues/505) |
| ラベルに出ない実行 (受け入れの確認のあとの手順など) を `agents` にどう載せるか | [#502](https://github.com/cloveclovedev/cumin-works/issues/502) |
| golden fileの置き場所と、Swiftのテストからの読み方 | [#503](https://github.com/cloveclovedev/cumin-works/issues/503)、[#504](https://github.com/cloveclovedev/cumin-works/issues/504) |
| 対応するmacOSとSwiftの最も古い版 (macOS 11.0 以上) | [#504](https://github.com/cloveclovedev/cumin-works/issues/504) |

## 後回しにしたこと

- `waiting[].url` を、mergeの判断を待つ実装IssueではPull RequestのURLにすること。きっかけ: OwnerがIssueからPull Requestへ移る手間を減らしたいと言ったとき。
- Issueの題名をファイルに載せること。きっかけ: 番号だけの行では、Ownerが見分けられないと分かったとき。
- Notification Centerの通知と、app bundle。きっかけ: 音と点滅では、Ownerが気付かないと分かったとき。
- `cumin setup` にアプリのLaunchAgentを書き出すサブコマンドを足すこと。きっかけ: 手でplistを置く手順 (#507) が、誤りのもとになったとき。
