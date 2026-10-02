# はじめに

リポジトリを取得してから、ビルドとテストを通し、`cumin` コマンドを動かすまでの手順。

## 必要なもの

- Git
- Go。必要なバージョンは、リポジトリの直下にある `go.mod` の `go` の行に書いてある

## 取得する

```sh
git clone https://github.com/cloveclovedev/cumin-works.git
cd cumin-works
```

## ビルドとテスト

次の4つが通ることを確かめる。`gofmt -l .` は、何も表示しなければ成功である。

```sh
go build ./...
go vet ./...
go test -race ./...
gofmt -l .
```

テストは、ネットワークも、Claude Codeの利用枠も使わない。

CI (GitHub Actions) も、Pull Requestとmainへのpushのたびに、同じ4つを実行する。CIはさらに、保護されたパスのworkflowのテスト (`python3 scripts/setup-repo/test_protected_paths.py`) を実行し、macOSのrunnerで `internal/platform/keychain` のテストを実行する。Keychainのテストは、macOSの `security` コマンドを使うので、Linuxでは飛ばされる。

## 動かす

```sh
go run ./cmd/cumin --help
```

サブコマンドの一覧 (`run`、`status`、`quota allow`、`stop`、`setup`) が表示される。`setup` には `github-apps` と `launchd` がある。それぞれの役割は [cumin本体の要件](requirements/cumin-core.md) の「動かし方」にある。

実行ファイルを作るときは、次のようにする。リポジトリの直下にできる `cumin` は、gitの管理から外してある。

```sh
go build -o cumin ./cmd/cumin
./cumin --help
```

Hostに置いて使うときは、`scripts/install.sh` を使う。ビルドして `~/.local/bin/cumin` に置く (`--prefix` で場所を変えられる)。`--restart` を付けると、launchd で動いている cumin を新しいバイナリに入れ替える。

## 今できること

`cumin run` は、常駐して定期確認を行う。今できるのは、着手 (I1)、Implementerの実行、実行のあとのPull Requestの検証 (I2)、必須のcheckが通ったIssueのReviewerへの依頼 (I3)、指摘の修正の依頼 (I5)、承認されたPull Requestのmerge (I6) とOwnerへの判断の依頼 (I7)、Ownerの承認のあとのmerge (I12)、上限での原因の整理 (I8) までである。あわせて、IssueのラベルをPull Requestにコピーし (I11)、mergeのあとに残った作業をフォローアップノートとして要求Issueにコピーする (I9)。設定ファイルの書き方は [設定の一覧](development/configuration.md) にある。

```sh
go run ./cmd/cumin run --config <設定ファイル>
```

起動すると、次の順に動く。

1. 設定ファイルを読み込んで検証する。問題があれば、キーの名前を表示して、0以外の終了コードで終わる。
2. 対象のリポジトリの持ち主ごとに、`github_apps.<owner>.<app>` の Client ID を4つの App (`cumin-core` と3つのrole) について確かめ、Keychain から秘密鍵を読む。Client ID か秘密鍵がなければ、キーの名前を表示して終わる。
3. Agentに渡すskillを、状態のディレクトリの下に書き出す。
4. Hostの状態ファイル (`~/.local/state/cumin/state.json`) を読む。Issueごとの、ImplementerとReviewerのセッションの番号と、checkの修正の回数が入っている。ファイルがなければ、空の状態で静かに始める (初回の起動がこれである)。壊れている、読めない、新しい版のときは、空の状態で始めて警告を1行出す。前の版のファイルは、そのまま読む。どちらでも失うのは、次の依頼が新しいセッションで始まり、回数が0に戻ることだけである。
5. 対象のリポジトリごとに、足りないラベル (`cumin/type/requirement`、`cumin/status/*`、`risk/*`) を作る。
6. `poll_interval` (初期値は60秒) ごとに定期確認を行う。要求Issueの分割と実装Issueの実装に着手する前には、Claude Code の最小の実行で使用率を読む。5h枠が時間帯のしきい値に、またはweekly枠がペースの上限に達していれば、ラベルを替えずに着手を飛ばし、枠ごとに1回だけOwnerに通知する (Q1)。使用率を読めなかったときも、着手せずに1回だけ通知する。Agentの実行が終わったときも、その実行の使用率で同じ判定をする。読んだ使用率はHostの状態ファイルに残る。止めている間は、残した使用率から次に試す時刻を決め、その時刻まで最小の実行をしない (Q3)。cuminを起動し直しても同じである。定期確認のたびに動作が1つもなく、実行中のAgentもいなければ、Ownerに「待ち状態」を1回だけ通知する (Q4)。cuminが何か動作をするまで、同じ通知を繰り返さない。`cumin/status/ready` の付いた要求Issueがあれば、ラベルを `cumin/status/planning` に替えてから、既定のブランチを detached で開いた worktree で Planner を起動し、分割を依頼する。`cumin/status/ready` の付いた実装Issueがあれば、ラベルを `cumin/status/implementing` に替えてから、`work_dir` の下に worktree を用意して、Implementer を起動する。そのIssueを閉じる開いているPull Requestが既にあれば、そのブランチの上で続きを依頼する。実行は定期確認とは別に進むので、定期確認は止まらない。実行が終わると、結果 (`done` か `blocked`) とセッションの番号、または異常終了の種類がログに出る。定期確認のたびに、実装Issueを閉じる開いているPull Requestの `cumin/status/*` と `risk/*` を、Issueと同じにする (I11)。要求Issueは、sub-issueに合わせてラベルが替わる。Ownerがsub-issueに `cumin/status/ready` を付けると `cumin/status/implementing` になり (R3)、状態ラベルのないsub-issueだけが残ると `cumin/status/awaiting-owner-review` に戻って、Ownerに通知が届く (R6)。sub-issueが全て閉じると、フォローアップノートを書き終えてから、Plannerに受け入れの確認を依頼し (R4)、Plannerの `## Acceptance check` のコメントが付くと `cumin/status/awaiting-owner-review` に替えて、Ownerに通知する (R7)。sub-issueが閉じると、そのworktreeとローカルのブランチ、状態ファイルの項目を消す。GitHubにない作業を持つworktreeは残し、ログに警告を出す。Plannerのworktreeは、実行が終わるたびに消す。mergeされたPull Requestがsub-issueを閉じると、その説明の `Follow-up` と、対応されなかった `(non-blocking)` の指摘を、フォローアップノートとして要求Issueに1回だけコメントする (I9)。拾うものがなければ書かない。
7. 結果が `done` なら、そのリポジトリを読み直して、cumin が決めたブランチに Implementer の App の開いているPull Requestがあること、worktree の先頭のコミットがpushされていることを確かめる (I2)。通れば、IssueにそのPull Requestを閉じるリンクがないときは `cumin-core` がリンクを付けて読み直し、ラベルを `cumin/status/awaiting-checks` に替える。Plannerの結果が `done` なら、要求Issueにsub-issueが1つ以上あり、それぞれに `risk/*` がちょうど1つ付いていることを確かめる (R2)。通れば、ラベルを `cumin/status/awaiting-owner-review` に替えて Owner に通知する。sub-issueが全て閉じているとき (受け入れの確認のあとの再開) は、`cumin/status/implementing` に替え、次の定期確認で受け入れの確認に戻る。
8. `cumin/status/awaiting-checks` のIssueがあるリポジトリでは、既定のブランチの必須のcheckの一覧を読む。Pull Requestの先頭のコミットで必須のcheckが全て通っていれば、ラベルを `cumin/status/reviewing` に替えて、Reviewerにレビューを依頼する (I3)。必須のcheckが1つもなければ、すぐ替える。Reviewerは、Pull Requestの先頭のコミットをdetachedで開いたworktree (`<Issue番号>-reviewer`) で動く。1ラウンド目は新しいセッション、2ラウンド目以降はReviewerのセッションの続きである。実行のあと、先頭のコミットにReviewerの `APPROVE` か `REQUEST_CHANGES` があるかを確かめ、なければ1回だけ依頼し直す。`APPROVE` なら、実装Issueのriskのラベルと必須のcheckを読み直す。`risk/low` なら `cumin-core` がmergeし、10秒待ってGitHubが実装Issueを閉じていなければ、1回だけ閉じる (I6)。`risk/medium` か `risk/high` なら、ラベルを `cumin/status/awaiting-owner-review` に替えて、Pull Requestのアドレスを通知する (I7)。mergeが衝突したときは、ラベルを `cumin/status/implementing` に戻し、Implementerのセッションの続きで衝突の解消 (既定のブランチのmerge) を依頼する。そのあとはI2、必須のcheck、I3と同じ道を通る。riskのラベルがちょうど1つでないときと、衝突のほかの理由でmergeか閉じる操作が失敗したときは、Ownerに戻す。Reviewerが `blocked` を返せばOwnerに戻す (I10)。 `cumin/status/awaiting-owner-review` の実装Issueでは、OwnerがPull Requestの先頭のコミットをGitHubのレビューで承認すると、cuminがレビューした人の権限を読み、Owner (writeかadminの人) の最新のレビューが承認で、必須のcheckが通っていれば、同じ手順でmergeする (I12)。`REQUEST_CHANGES` で、ラウンドが `max_review_rounds` 未満なら、ラベルを `cumin/status/implementing` に戻し、Implementerのセッションの続きで指摘の修正を依頼する (I5)。修正のあとは、I2、必須のcheck、I3と同じ道を通って、次のラウンドのレビューになる。上限のラウンドでも `REQUEST_CHANGES` なら、Reviewerに原因の整理を依頼し、Pull Requestに書かれたOwner向けのコメントを確かめてから、`cumin/status/awaiting-owner-decision` に替えて通知する (I8)。必須のcheckが落ちていれば、ラベルを `cumin/status/implementing` に戻し、落ちたcheckの内容を添えて、直前の実行のセッションのまま Implementer に修正を依頼する (I4)。依頼した回数はHostの状態ファイルに残り、`max_check_fix_requests` に達すると、依頼せずにOwnerに戻す。
9. 実行が異常終了したときは、同じ依頼を同じ作業場所で1回だけやり直す。新しいAgentの実行なので、利用枠を使う。cuminを止めたときの異常終了は、やり直さない。
10. 先に進めないときは、Ownerに戻す。Issueにコメントを付け、ラベルを `cumin/status/awaiting-owner-decision` に替えて、Ownerに通知する。コメントは、結果が `blocked` ならAgentが返した理由そのもの、それ以外なら cumin が書く (どの確認で落ちたか、または2回の異常終了の種類)。`blocked` はやり直さない。
11. リポジトリの定期確認が、同じ理由で3回続けて失敗したら、1回だけ通知する。次に知らせるのは、そのリポジトリの定期確認が成功したあとである。
12. 通知は Discord の webhook で届く。アドレスは Keychain にあり ([セットアップの手順](development/setup-guide.md))、出すかどうかは設定 `notify.discord.enabled` が決める。アドレスがなくても cumin は起動し、起動のログに警告が出る。

Implementer の実行は、本物の Claude Code を起動し、利用枠を使う。

ログは、JSONで標準出力に出る。1回の定期確認ごとに1行 (リポジトリ、GraphQLの `cost` と `remaining`)、着手と依頼、実行の終わりのたびに1行が出る。token や鍵は出ない。

止めるには、Ctrl-C (SIGINT) か SIGTERM を送る。cumin は新しい着手をやめ、実行中の依頼を取り消し、猶予の間だけ終わるのを待つ。猶予は、Agent の猶予 (10秒) に、そのあとの後始末の分 (5秒) を足した値である。最後に `stopped` のログを1行出して、終了コード0で終わる。ログには、進行中だったIssueの一覧が入る。ラベルは変わらないので、途中で止まったIssueは `cumin/status/implementing` のまま残る。Ownerが `cumin/status/ready` を付け直すと、次の定期確認で着手し直す。

実行中のAgentを途中で止めたくないときは、Hostで `cumin stop --after-current-runs` を実行する。コマンドは、止める予約 (`~/.local/state/cumin/drain-request.json`) を書いてすぐ終わる。`cumin run` は、次の定期確認から新しい依頼 (分割、受け入れの確認、実装、レビュー、checkの修正) を始めず、実行中の実行と、その終わりに続く動作を済ませる。その間も、Agentの要らない動作 (ラベルの付け替え、Ownerの承認のあとのmerge、フォローアップノート) は続く。実行中のものがなくなると、定期確認をもう1回行い、予約を消し、`stopped` のログ (理由は drain) を出して、終了コード0で終わる。作業中のラベルのまま残るIssueはないので、起動し直せば続きから進む。途中で SIGTERM を送ると、上のとおりすぐに止まる。予約は次の起動に残らない。決まりは [cumin本体の設計メモ](designs/cumin-core.md) の「実行を待ってから止める」にある。

`--config` を省くと、`~/.config/cumin/config.toml` を読む。cumin を止めたときに `cumin/status/implementing` のまま残ったIssueは、自動では回収されない。Ownerが `cumin/status/ready` を付け直すと、次の定期確認で着手し直す ([Issueのラベルと状態遷移](requirements/workflow/issue-states.md) の「v0.1では実装しないこと」)。

常駐させるときは、ターミナルではなく launchd から起動する。`cumin setup launchd` が、今のユーザの LaunchAgent を書き出し、`launchctl` のコマンドを表示する。手順は [セットアップの手順](development/setup-guide.md) の手順4にある。launchd から動かすと、ログは標準出力ではなく `~/.local/state/cumin/cumin.log` に出る。

5h枠のしきい値で着手が止まったとき、その5h枠を使い切ってよければ、Hostで `cumin quota allow` を実行する (Q2)。`cumin run` が最後に読んだ5h枠のリセット時刻を、`~/.local/state/cumin/quota-allowance.json` に書く。次の定期確認から、その時刻まで5h枠のしきい値が100%になる。weekly枠のペースの上限は変わらない。`cumin run` がまだ使用率を読んでいないとき、または最後に読んだ5h枠が既にリセットされたときは、何も書かずに0以外の終了コードで終わる。

`cumin status` は、今の状態を表示する。Agentが動いているIssueとOwnerの対応を待つIssue (GitHubのラベルから読む)、`cumin run` が最後に読んだ両方の枠の使用率とその時刻、今の上限、許可、新しい着手が止まっているかどうかと次に試す時刻である。`cumin stop --after-current-runs` の予約があるあいだは、止まる途中であることと、予約した時刻も表示する。使用率の数値はターミナルにだけ出し、ログには出さない。`cumin run` と同じく `--config` で設定ファイルを変えられる。Keychainの秘密鍵を読むので、`cumin run` と同じユーザで実行する。

`cumin --version` は、cuminの版と、ビルドしたコミットを表示する。

サブコマンドを作るたびに、このページを更新する。
