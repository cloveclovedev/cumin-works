# Hostでの動かし方

cuminをHostでどう動かし、どう止め、何で設定するかを決める。Hostの上の操作はOperatorが受け持つ（[権限](../access/index.md)の`CWK-ACCESS-003`）。

## 全体像

cuminは、Hostのユーザアカウントで常駐し、自分からGitHubを確かめて作業を進める。Hostに置くものは、次の4種類である。

![Hostの全体像](overview.svg)

図の元ファイル: [overview.puml](overview.puml)

- 設定: Operatorが書く。秘密の値は含めない。
- 秘密の値: macOSのKeychainに置く
- 手元の状態: cuminが書く。失っても作業を失わない（[定期確認](../poll/index.md)の`CWK-POLL-003`）。
- モニターファイル: cuminが書き、cuminを外から見る道具が読む

## 要件

### 常駐

#### `CWK-HOST-001` 常駐する

cuminは、HostのユーザアカウントのlaunchdのLaunchAgentとして常駐する。0以外の終了コードで終わったときだけ、launchdが起動し直す。

- 検証: `demo` cuminのプロセスを異常終了させるとlaunchdが起動し直し、SIGTERMで止めたときは起動し直さない

---

#### `CWK-HOST-002` ログ

cuminは、ログをJSONの構造化ログとして標準出力に出す。秘密の値（token、秘密鍵、webhookのアドレス）は、どのレベルのログにも出さない。

- 検証: `test` Agent起動とマージを含む定期確認のログが、すべてJSONの行で、秘密の値を含まない

---

### コマンド

#### `CWK-HOST-003` コマンドの一覧

Operatorは、次のコマンドを使う。

| コマンド | すること |
|---|---|
| `cumin run` | 常駐して動く。launchdから起動する |
| `cumin status` | 今の状態を表示する（`CWK-HOST-004`）|
| `cumin quota allow` | 今の5時間制限を使い切ってよいと許可する（[利用制限](../limit/index.md)の`CWK-LIMIT-006`）|
| `cumin stop --after-current-runs` | 動いているAgentの実行が終わるのを待ってから止める（`CWK-HOST-007`）|
| `cumin setup github-apps` | roleごとのGitHub Appを登録し、秘密鍵をKeychainに入れる。もう一度実行すると、足りないGitHub Appだけを登録する |
| `cumin setup launchd` | LaunchAgentの定義を書く（`CWK-HOST-005`）|
| `cumin setup notify --discord-webhook` | 通知のwebhookのアドレスを標準入力から受け取り、Keychainに入れる |
| `cumin --version` | cuminの版を表示する |

- 検証: `inspection` `cumin --help`の一覧が、この表と一致する

---

#### `CWK-HOST-004` 状態の表示

`cumin status`は、次のことを表示する。GitHubのラベル、手元の状態、モニターファイル（`CWK-HOST-014`）から読み、GitHubに書き込まない。

- 作業中のAgentと、`awaiting-`で始まる状態のIssue
- 5時間制限と週間制限の使用率、読んだ時刻、今の上限、Agent起動を止めているかとその原因
- 止める予約があるか
- 失敗しているツール動作確認と、全部を読めないIssue。Pull Requestの数で全部を読めないIssueは、通知だけで知らせる
- 最後の定期確認の時刻と、そのエラー。モニターファイルがないとき、または最後の定期確認が`poll_interval`の3倍より古いときは、そのこと

モニターファイルの中身で、終了コードは変えない。

- 検証: `test` 使用率でAgent起動を止めているとき、`cumin status`がその原因の制限を表示する
- 検証: `test` モニターファイルがないとき、そのことを1行で表示し、ほかの表示は続ける

---

#### `CWK-HOST-005` LaunchAgentの定義を書く

`cumin setup launchd`は、実行中のcuminの絶対パスと、コマンドを実行したシェルの`PATH`を使って、LaunchAgentの定義を書く。書く前に、cumin自身が起動するコマンド（roleごとのAgentのCLIと`git`）が、その`PATH`にあるかを確かめる。1つでもなければ、その名前を示して、何も書かずに失敗で終わる。すでにある定義と違うときは、差分を示して、上書きしない。

- 検証: `test` AgentのCLIが`PATH`にないと、その名前を示して、定義を書かずに失敗で終わる

---

### 止め方

#### `CWK-HOST-006` すぐに止める

`cumin run`は、SIGINTとSIGTERMで、動いているAgentも止めてすぐに終わる。ラベルは替えず、終了コードは0なのでlaunchdは起動し直さない。

- 検証: `test` Agentの実行中にSIGTERMを送ると、Agentのプロセスも残らずに、終了コード0で終わる。起動し直すと、そのIssueのやり直しが起きる

---

#### `CWK-HOST-007` 実行を待ってから止める

`cumin stop --after-current-runs`を実行すると、cuminは新しいAgent起動をせず、動いているAgentの実行が終わるのを待ってから止まる。待つ間も、Agentを起動しないアクション（ラベルの付け替え、コメント、通知、マージ）は続ける。止める予約は、次の起動には残らない。

- 検証: `test` 予約のあと、動いている実行の結果はラベルに反映されるが、続きのAgent起動は起きず、終了コード0で終わる。起動し直すと、続きのAgent起動が1回だけ起きる

---

#### `CWK-HOST-008` スリープ

Hostがスリープから戻ったとき、cuminと動いているAgentは続きから動く。戻ったことで起きた失敗（通信の失敗、tokenの失効、時間の上限）は、Agentの異常終了として扱う（[Agent](../agent/index.md)の`CWK-AGENT-017`）。

- 検証: `demo` Agentの実行中にHostをスリープさせ、戻ったあと、そのIssueが異常終了のやり直しで進む

---

### 設定

#### `CWK-HOST-009` 設定ファイル

設定はTOMLのファイルに書く。値をコードに埋め込まない。Hostの設定ファイルは`~/.config/cumin/config.toml`にあり、`cumin run --config`で変えられる。知らないキー、型の違う値、範囲を外れた値があると、`cumin run`は起動せずに、そのキーを示して終わる。

- 検証: `test` 知らないキーと範囲を外れた値がある設定ファイルで、`cumin run`が起動せずに、それぞれのキーを示す

---

#### `CWK-HOST-010` リポジトリの設定

対象のリポジトリは、mainブランチの`.cumin/config.toml`で、一部の設定を自分で決められる。Pull Requestのブランチの内容は効かない。`.cumin/`は保護されたパスなので、Maintainerが承認したものだけが効く。

- 優先順位は、初期値、Hostの設定ファイル、リポジトリの設定の順に強くなる
- リポジトリの設定に、Hostだけのキー、知らないキー、範囲を外れた値があると、cuminはそのキーをログに出して、そのリポジトリの定期確認を飛ばす。ほかのリポジトリは続ける。

- 検証: `test` リポジトリの設定で`max_review_rounds`を変えると、そのリポジトリだけに効く
- 検証: `test` リポジトリの設定に`work_dir`があると、そのリポジトリの定期確認だけが飛ばされる

---

#### `CWK-HOST-011` 設定の一覧

設定のキーは、次のとおりである。「書く場所」の「Host」はHostの設定ファイルだけ、「両方」はHostの設定ファイルとリポジトリの設定、「リポジトリ」はリポジトリの設定だけに書けることを表す。

| キー | 内容 | 初期値 | 書く場所 |
|---|---|---|---|
| `repositories` | 対象のリポジトリの一覧 | なし（必須）| Host |
| `work_dir` | 作業ディレクトリ | なし（必須）| Host |
| `poll_interval` | 作業中のリポジトリの定期確認の間隔（`CWK-POLL-004`）| 60秒 | Host |
| `idle_poll_interval` | 作業中でないリポジトリの定期確認の間隔。`poll_interval`以上（`CWK-POLL-004`）| 5分 | Host |
| `max_issues_in_progress` | リポジトリごとに同時に進めるIssueの数（`CWK-POLL-005`）| 1 | Host |
| `quota.five_hour.threshold` | 5時間制限のしきい値（`CWK-LIMIT-004`）| 85% | Host |
| `quota.five_hour.bands` | 5時間制限の時間帯ごとのしきい値（`CWK-LIMIT-004`）| なし | Host |
| `quota.weekly.target` | 週間制限の目標（`CWK-LIMIT-005`）| 85% | Host |
| `quota.weekly.lead` | 週間制限の前倒し。7日未満（`CWK-LIMIT-005`）| 1日 | Host |
| `github_apps.<organization>.<app>` | GitHub AppのClient ID。`cumin setup github-apps`が書く | なし | Host |
| `roles.<role>.time_limit` | 実行の時間の上限。55分以下（`CWK-AGENT-015`）| 50分 | Host |
| `roles.<role>.cli_path` | AgentのCLIの実行ファイル | `claude` | Host |
| `roles.<role>.cli` | Agentを動かすCLI。今はClaude Codeだけ（`CWK-AGENT-019`）| Claude Code | 両方 |
| `roles.<role>.model` | Agentを動かすモデル。空ならCLIの既定 | 空 | 両方 |
| `max_review_rounds` | レビューのラウンドの上限（[レビュー](../review/index.md)）| 5 | 両方 |
| `max_check_fix_requests` | チェックの修正を頼む回数の上限（[チェック](../check/index.md)）| 3 | 両方 |
| `checks_wait_time` | 必須チェックの結果を待つ時間（[チェック](../check/index.md)）| 60分 | 両方 |
| `merge_method` | マージの方法。squash、merge、rebaseのどれか（[マージ](../merge/index.md)）| squash | 両方 |
| `notify.discord.enabled` | 通知を送るか。リポジトリで決めると、そのリポジトリについての通知に効く（[通知](../notify/index.md)）| true | 両方 |
| `priority_labels` | 優先度のラベルの名前。高い順（`CWK-LABEL-008`）| `cumin/priority/P0`〜`P3` | リポジトリ |
| `protected_paths` | 保護されたパス（`CWK-CHECK-008.1`）| `.cumin/`、`CLAUDE.md`、`AGENTS.md`、`.claude/`、`REVIEW.md` | リポジトリ |
| `tool_checks` | ツール動作確認（`CWK-AGENT-018.1`）| なし | リポジトリ |
| `start_on_awaiting_merge_decision` | マージ判断を待つPull Requestのブランチから始めるか（`CWK-IMPL-015`）| false | リポジトリ |

- `<role>`は`planner`、`implementer`、`reviewer`のどれか、`<app>`は`cumin-core`とこの3つのどれかである
- 時間は、`"60s"`や`"50m"`のように、単位を付けて書く
- リポジトリだけのキーは、Hostの設定ファイルに書くと、知らないキーの誤りになる
- 次の値は設定にせず、固定の値とする: GitHubの読み取りのやり直しの回数と間隔（`CWK-POLL-007`）、sub-issueを読む数（`CWK-POLL-011`）、続けて失敗したら知らせる回数（`CWK-POLL-010`）、1回の定期確認で2つ以上マージするときの間に待つ秒数（`CWK-MERGE-007`）

- 検証: `test` この表のすべてのキーを書いた設定ファイルを、cuminが誤りなく読む
- 検証: `test` リポジトリだけのキーをHostの設定ファイルに書くと、知らないキーの誤りになる

---

#### `CWK-HOST-012` リスクの基準

リスクの基準は、TOMLの値ではなく、Markdownのファイルで決める。cuminは中身を読まず、PlannerとReviewerへの指示にそのまま入れる（[Agent](../agent/index.md)の`CWK-AGENT-006`）。

| 段 | 場所 |
|---|---|
| 初期値 | cuminに入っている`disciplines/software-engineering/risk-criteria.md` |
| Host | Hostの設定ファイルと同じディレクトリの`risk-criteria.md` |
| リポジトリ | 対象のリポジトリのmainブランチの`.cumin/risk-criteria.md` |

強い段のファイルがあれば、弱い段の基準を丸ごと置き換える。中身が空白だけのファイルは、そのリポジトリの設定の誤りとする。

- 検証: `test` リポジトリに`.cumin/risk-criteria.md`があると、Plannerへの指示に、その中身だけが入る

---

### 秘密の値と外から見るためのファイル

#### `CWK-HOST-013` 秘密の値

GitHub Appの秘密鍵と、通知のwebhookのアドレスは、HostのmacOSのKeychainに置く。設定ファイルとリポジトリには書かない。cuminは起動のときに読み、メモリにだけ持ち、手元の状態にもログにも書かない。

- 検証: `test` 秘密の値が、設定ファイル、手元の状態、ログのどれにも現れない

---

#### `CWK-HOST-014` モニターファイル

cuminは、定期確認のたびとAgentの実行が終わるたびに、モニターファイルを書き直す。中身は、最後の定期確認の時刻とエラー、止める予約、利用制限で止めているか、失敗しているツール動作確認、全部を読めないIssue、動いているAgent、`awaiting-`で始まる状態のIssueである。cuminを外から見る道具と`cumin status`が読む。cumin自身は読まず、ここからは何も決めない。

- 検証: `test` 定期確認のあと、モニターファイルに、その時刻と、`awaiting-`で始まる状態のIssueがある
- 検証: `test` モニターファイルを消しても、cuminのアクションは変わらない

---

## 受け入れテスト

| # | 場面 | 期待する結果 |
|---|---|---|
| 1 | Hostにcuminを入れ、`cumin setup github-apps`、`cumin setup launchd`を実行して、ログインする | cuminが常駐して、対象のリポジトリの定期確認を始める（`CWK-HOST-001`、`CWK-HOST-003`、`CWK-HOST-005`）|
| 2 | Agentの実行中に`cumin stop --after-current-runs`を実行し、cuminの版を入れ替えて起動する | 実行の結果が反映されてから止まり、起動のあとに続きから進む（`CWK-HOST-007`）|
| 3 | リポジトリの`.cumin/config.toml`に誤りのある値を足す | そのリポジトリの定期確認だけが飛ばされ、Maintainerが直すと次の定期確認から戻る（`CWK-HOST-010`）|
| 4 | AgentのCLIか`git`が`PATH`にない状態で`cumin setup launchd`を実行する | ないコマンドを示して、定義を書かずに失敗で終わる（`CWK-HOST-005`）|

## 未決事項

なし
