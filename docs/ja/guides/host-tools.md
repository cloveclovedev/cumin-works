# Hostの一般の道具

MaintainerやOperatorが、Claude Codeなしで端末から使う道具の使い方。道具は `scripts/` のスクリプトで、どれも待つ時間に上限がある。Maintainerのセッションのskill ([Maintainerのセッションにskillを入れる](maintainer-skills.md)) は、同じ道具を呼ぶだけで、自分の手順を足さない。

## 一般の道具とは何か

![MaintainerとClaude Codeのセッションが、scripts/ の3つの一般の道具を呼ぶ。cumin-health.sh は、cumin run が書くモニターファイルとログを読む。replace-binary.sh は、cuminを止め、新しいバイナリを入れ、起動し直し、cumin-health.sh を呼ぶ。live-scenario.sh は、あとのIssueで加わる](host-tools.svg)

図の元ファイル: [host-tools.puml](host-tools.puml)

| 道具 | すること | 今あるか |
|---|---|---|
| `scripts/cumin-health.sh` | cuminが動いているかを言う。最後の定期確認、起動してからのエラー、実行中のAgent | ある (この文書の「cuminが動いているか確かめる」) |
| `scripts/replace-binary.sh` | 実行を待ってcuminを止め、新しいバイナリを入れ、起動し直し、定期確認を2回確かめる | ある (この文書の「バイナリを入れ替える」) |
| `scripts/live-scenario.sh` | Hostの設定をsandboxに向けて実機のシナリオを実行し、結果にかかわらず設定を戻す | まだない。あとのIssueで加わる (図の点線の矢印) |

- 道具は、cuminの判断を変えない。`cumin-health.sh` は読むだけで、何も書かず、GitHubに問い合わせない。`replace-binary.sh` は、Hostのcuminを止めて入れ替えるが、GitHubのラベルやIssueには触れない。
- 道具は、1つのOrganizationの事実 (リポジトリ、App、人の名前) を持たない。

## cuminが動いているか確かめる

「cuminが動いているか」には、3つの部分がある。最後の定期確認、起動してからのエラー、実行中のAgentである。`cumin status` は実行中のAgentを、メニューバーのアプリ ([メニューバーのアプリを作って起動する](status-menu-bar.md)) は最後の定期確認を示す。`scripts/cumin-health.sh` は、3つをまとめて1回で出し、終了コードで答える。

### 必要なもの

- macOS。スクリプトは、macOSに入っている道具 (`sh`、`plutil`、`date`、`sed`、`awk`、`sleep`、`tr`) だけを使う。JSONも `plutil` で読むので、`jq` は要らない。
- `cumin run` が1回以上定期確認を終えていること。モニターファイル (`~/.local/state/cumin/monitor.json`) は、そのときにできる。
- 起動してからのエラーを見るには、LaunchAgentのplist (`~/Library/LaunchAgents/dev.cloveclove.cumin.plist`、[セットアップの手順](../development/setup-guide.md))。plistがなくても、ほかの行は出る。

### 実行する

リポジトリの先頭で実行する。

```sh
scripts/cumin-health.sh
```

出力の例:

```text
last poll: 2026-10-04T07:00:05Z (55s ago)
errors of the last poll: 1
  example/app: read the snapshot: GitHub returned 502
errors since the start: 1 (log: <home>/.local/state/cumin/cumin.log)
  2026-10-04T06:20:00Z the poll failed
agents at work: 2
  example/app#31 planner (acceptance check): Show the history of an item
  example/tool#12 implementer (implement): feat(api): add the list endpoint
waiting issues: 2
error: the last poll has 1 error(s)
```

| 行 | 出どころ | 意味 |
|---|---|---|
| `last poll` | モニターファイルの `last_poll.at` | 最後の定期確認の1回りが終わった時刻と、今までの秒数 |
| `errors of the last poll` | モニターファイルの `last_poll.errors` | 最後の確認が失敗したままのリポジトリと、その理由 |
| `errors since the start` | LaunchAgentのログ | 最後の `cumin run starts` の行よりあとの、levelが `ERROR` の行の数と、各行の時刻とメッセージ |
| `agents at work` | モニターファイルの `agents` | 実行中のAgent。リポジトリ、Issueの番号、role、依頼の種類、題名 |
| `waiting issues` | モニターファイルの `waiting` | Maintainerの対応を待つIssueの数。中身は、メニューバーのアプリか `cumin status` で見る |
| 最後の行 | 上の行から | 動いていれば `cumin is running: ...`、そうでなければ `error:` と理由 |

- ログの場所は、plistの `StandardOutPath` から読む。スクリプトは場所を決め打ちしない。
- ログの行からは、時刻とメッセージだけを出す。ほかのフィールドは出さない。モニターファイルにも使用率は載らないので ([モニターファイルとメニューバーのアプリの設計](../designs/status-menu-bar.md))、スクリプトは使用率の数値を出さない。
- plistがない、ログが読めない、ログに `cumin run starts` の行がないときは、`errors since the start: unknown` と理由を出す。
- モニターファイルの各フィールドの意味は、[モニターファイルとメニューバーのアプリの設計](../designs/status-menu-bar.md) にある。

### 終了コード

| 終了コード | いつ |
|---|---|
| `0` | 最後の定期確認が新しく、エラーがない |
| `1` | 最後の定期確認が古い。最後の定期確認にエラーがある。モニターファイルがない、または読めない。`--wait-polls` が上限で終わった、または待った定期確認にエラーがあった |
| `2` | オプションの誤り。知らないオプション、値のないオプション、数でない値 |

- 「新しい」とは、今の時刻から `last_poll.at` を引いた値が上限以下であることである。上限の初期値は180秒で、メニューバーのアプリの `stale_after_sec` の初期値と同じである。`poll_interval` を長くしたHostでは、`--stale-after` で上限も長くする。
- 古いときは、cuminが止まったか、Hostがスリープしたかである。`launchctl print gui/$(id -u)/dev.cloveclove.cumin` で、jobの状態を確かめる。
- 起動してからのエラーは、表示するだけで、終了コードを変えない。前の定期確認のエラーは、次の定期確認が成功すれば済んだことだからである。
- モニターファイルの `last_poll.errors`、`agents`、`waiting` のどれかがない、または配列でないときも、読めないものとして `1` で終わる。エラーも実行中のAgentもないとは見なさない。
- 題名とメッセージは、制御文字を除いて、そのまま出す。
- モニターファイルの `version` が、スクリプトの知る版より新しいときも、読めないものとして `1` で終わる。

### オプション

| オプション | 意味 | 初期値 |
|---|---|---|
| `--stale-after <seconds>` | 最後の定期確認を古いとする上限 (秒) | `180` |
| `--wait-polls <n>` | 報告の前に、`last_poll.at` がn回進むのを待つ | 待たない |
| `--timeout <seconds>` | `--wait-polls` の待つ時間の上限 (秒) | `300` |
| `--monitor <file>` | モニターファイルの場所 | `~/.local/state/cumin/monitor.json` |
| `--plist <file>` | LaunchAgentのplistの場所 | `~/Library/LaunchAgents/dev.cloveclove.cumin.plist` |

### 次の定期確認を待つ

バイナリを入れ替えたあとなど、これからの定期確認が成功することを確かめたいときに使う。

```sh
scripts/cumin-health.sh --wait-polls 2 --timeout 300
```

- スクリプトは、`last_poll.at` が進むたびに `poll 1 of 2: <時刻>, no error` のように1行出す。n回進んだら、いつもの報告を出し、いつもの終了コードで終わる。
- 待った定期確認の1つにエラーがあれば、残りを待たずに報告を出し、`1` で終わる。
- 上限の時間が過ぎたら、報告を出し、`error: time limit: the last poll moved 1 time(s) of 2 in 300s` のように、上限で終わったことと進んだ回数を言って、`1` で終わる。上限のない待ちはない。
- `--timeout` は、`poll_interval` のn回分より長くする。初期値の300秒は、`poll_interval` の初期値 (60秒) の2回分に余裕を足したものである。

## バイナリを入れ替える

コードを変えるPull Requestがmergeされても、Hostのcuminは古いバイナリのまま動く ([セットアップの手順](../development/setup-guide.md) の手順4)。`scripts/replace-binary.sh` は、入れ替えの手順を1回で行う。実行中のAgentを取り消さず、どの待ちにも上限がある。

### 必要なもの

- macOS。スクリプトは、`git`、`cumin`、macOSに入っている道具 (`sh`、`launchctl`、`plutil`、`date`、`sed`、`sleep`、`dirname`、`id`) だけを使う。手順4と手順5は `scripts/install.sh` と `scripts/cumin-health.sh` を呼ぶので、ビルドのための `go` も要る。
- cuminのリポジトリのチェックアウト。スクリプトは、自分の置かれたチェックアウトからビルドする。
- LaunchAgentが読み込まれていること ([セットアップの手順](../development/setup-guide.md) の手順4)。

### 実行する

mergeされた変更を確かめ、チェックアウトを既定のブランチの先頭に合わせてから (`git pull --ff-only`)、実行する。どのディレクトリからでもよい。

```sh
scripts/replace-binary.sh
```

スクリプトは、次の5つをこの順に行う。1つが失敗したら、そこで終わり、あとの手順を行わない。

| 手順 | すること | 失敗したとき |
|---|---|---|
| 1 | チェックアウトを確かめる。既定のブランチ (`origin/HEAD` の指すブランチ) にいる。手元の変更がない。`git fetch` のあとで、リモートの先頭と同じコミットである | cuminは動いたままである |
| 2 | 入れ替える先を確かめる。LaunchAgentが読み込まれていて、plistの `ProgramArguments` の先頭が `<prefix>/cumin` (または同じファイル) である | cuminは動いたままである |
| 3 | `cumin stop --after-current-runs` を実行し、cuminが終わるのを待つ | 上限で終わったら、cuminはそのままである。何も入れない |
| 4 | `scripts/install.sh --prefix <prefix> --restart` を実行する。ビルドして置き、止まっているjobを起動する | cuminは止まっている。原因を直して、表示されたコマンドを実行する |
| 5 | `scripts/cumin-health.sh --wait-polls 2` を実行する。定期確認が2回、エラーなしで終わることを確かめる | 新しいバイナリは入っていて、cuminは起動している。定期確認のエラーが出る |

- 手順1と手順2は、cuminを止める前に行う。ブランチや入れ替える先が違うときに、cuminを止めたままにしないためである。
- 手順3の待ちは、止める予約のファイル (`~/.local/state/cumin/stop-request.json`) がなくなるのを待つ。`cumin run` は、止まり終えるときにこのファイルを消す ([cumin本体の設計メモ](../designs/cumin-core.md) の「実行を待ってから止める」)。スクリプトはGitHubに問い合わせない。
- 手順3が上限で終わったら、`error: time limit: cumin has not ended in 3600s. ...` と出して、`1` で終わる。cuminは、実行を待ってから止まる途中のままである。もう一度実行すると、続きを待つ。
- cuminが動いていないHostでは、予約を消すものがいないので、手順3は上限で終わる。そのときは、`scripts/install.sh --restart` で入れて起動する。
- 手順5が失敗したときは、`cumin-health.sh` の出した定期確認のエラーのあとに、`error: the new binary is installed and cumin was started, but the check of the polls failed. ...` と出る。

### オプション

| オプション | 意味 | 初期値 |
|---|---|---|
| `--prefix <dir>` | バイナリを置くディレクトリ。`scripts/install.sh` にそのまま渡す | `~/.local/bin` |
| `--stop-timeout <seconds>` | 手順3の待つ時間の上限 (秒) | `3600` |
| `--dry-run` | 読むだけの確認を行い、残りの手順を表示して、何も変えない | 付けない |

- `--dry-run` は、手順1のブランチと手元の変更の確認と、手順2の確認を行う。`git fetch`、`cumin stop`、`scripts/install.sh`、`scripts/cumin-health.sh` は実行せず、実行するはずのコマンドを `dry run:` の行で表示する。確認が失敗したら、`1` で終わる。
- `--stop-timeout` の初期値の3600秒は、Agentの1回の実行が1時間近くかかることがあるからである。手順5の上限は、`cumin-health.sh` の `--timeout` の初期値 (300秒) である。

### 終了コード

| 終了コード | いつ |
|---|---|
| `0` | 5つの手順がすべて通った。`--dry-run` では、読むだけの確認が通った |
| `1` | 手順のどれかが失敗した。手順3が上限で終わった。必要な道具 (`git`、`cumin`、`launchctl`、`plutil`) がない |
| `2` | オプションの誤り。知らないオプション、値のないオプション、数でない `--stop-timeout` |
