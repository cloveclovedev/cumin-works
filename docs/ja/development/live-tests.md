# 実機の確認 (live test)

本物の GitHub App と、使い捨てのリポジトリ (sandbox) を使って、公式ドキュメントでは分からない GitHub の振る舞いを確かめるテスト。ふだんの `go test ./...` では動かない。

## いつ実行するか

- 利用枠を使うが、Operator に聞かずに実行してよい。何時間もかかる実行だけは、先に Operator に確かめる。
- ある Organization に cumin-works を導入した直後に、セットアップが正しいことを確かめるために実行する。
- GitHub の振る舞いが変わった疑いがあるときに、実行し直す。

ほとんどのテストは Claude Code を起動しないので、利用枠を使わない。起動するものには、下の表と「実行のしかた」で断りを書く。

## 前提

- [セットアップの手順](setup-guide.md) の手順1〜3が済んでいる。
  - 4つの GitHub App が登録され、sandbox のリポジトリにインストールされている。
  - Host の設定ファイルに Client ID があり、Keychain に秘密鍵がある。
  - sandbox に `scripts/setup-repo.sh <owner>/<repo> --core-app <slug> --implementer-app <slug>` を実行してある。
- sandbox は、公開のリポジトリである。App の token には Checks の権限がなく、check の結果を読めるのは公開のリポジトリだけだからである。テストは最初に、認証なしでリポジトリを読めることを確かめ、読めなければ止まる。
- sandbox の保護されたパスの一覧 (`.cumin/config.toml` の `protected_paths`) に `CLAUDE.md` があり、`live/` を守っていない。テストは `live/CLAUDE.md` (保護されている) と `live/<日時>.md` (保護されていない) を使う。ファイルがなければ、初期値の一覧が使われるので、そのままでよい。合っていなければ、テストは Pull Request を作る前に止まる。
- `TestLiveGitHubFacts` には、sandbox に次の2つが要る。リポジトリの管理者が用意する。
  - `internal/platform/github/testdata/cumin-live-fixture.yml` を `.github/workflows/cumin-live-fixture.yml` として、既定のブランチに置く。main は保護されているので、Pull Request で入れる。
  - `scripts/setup-repo.sh <owner>/<repo> --core-app <slug> --required-check live-skipped-for-bots --required-check live-check-1-required-line` を実行して、fixture の2つの job を必須のcheckにする。このスクリプトは必須のcheckの一覧を丸ごと置き換えるので、2つとも毎回渡す。`live-check-1-required-line` は場面 Check-1 が使う。
  - 足りなければ、テストは最初に止まって、足りないものを表示する。
- sandbox は、壊れてもよいリポジトリである。テストは Issue、Pull Request、ブランチ、ラベルを作り、main に小さなファイルを1つ merge する。

## 実行のしかた

```sh
CUMIN_LIVE=1 CUMIN_LIVE_REPO=<owner>/<repo> CUMIN_LIVE_OWNER=<login> go test -count=1 -run TestLive -v ./internal/platform/github/
# Agent の環境の確認 (internal/agent。Claude Code は起動しない)
CUMIN_LIVE=1 CUMIN_LIVE_REPO=<owner>/<repo> go test -race -count=1 -run TestLive_AgentEnvironment -v ./internal/agent/
# 本物の Claude Code に commit、push、Pull Request をさせる確認 (利用枠を使う)
CUMIN_LIVE=1 CUMIN_LIVE_REPO=<owner>/<repo> go test -race -count=1 -run TestLive_AgentRunOnSandbox -v ./internal/agent/
# 場面 E2E-1 (利用枠を使い、1時間ほどかかる。先に「実機の場面 E2E-1」の準備をする)
CUMIN_LIVE=1 CUMIN_LIVE_REPO=<owner>/<repo> go test -count=1 -timeout 4h -run TestLiveE2E -v ./cmd/cumin/
```

| 環境変数 | 内容 |
|---|---|
| `CUMIN_LIVE` | `1` のときだけ実行する。それ以外では skip する |
| `CUMIN_LIVE_REPO` | sandbox のリポジトリ。`<owner>/<repo>` の形 |
| `CUMIN_CONFIG` | Host の設定ファイル。省くと `~/.config/cumin/config.toml` |
| `CUMIN_LIVE_OWNER` | `TestLiveMergeFacts` と `TestLiveReviewRequestFacts` に要る。sandbox に admin か write の権限を持つ、人の GitHub の login。`TestLiveMergeFacts` は、この login の権限を cumin-core の App で読む。`TestLiveReviewRequestFacts` は、この login にレビューを依頼するので、その人に通知が届く |
| `CUMIN_LIVE_MENTION` | 任意。GitHub の login。指定すると、`TestLiveGitHubFacts` が、その人を@メンションするコメントを1つ投稿する。通知が届いたかは、その人が目で確かめる |

テストの最後に、結果の表 (番号、確かめたこと、期待、実際の結果) が Markdown で出力される。

## token の扱い

- テストは、Host の設定の Client ID と Keychain の秘密鍵から、App ごとに installation access token を発行する。token は、sandbox のリポジトリ1つと、その App の権限に絞られる。
- token は、テストのプロセスの中だけで使う。コマンドの引数、リモートのアドレス、ファイル、テストの出力には現れない。
- git には、環境変数 (`GIT_CONFIG_*`) で認証のヘッダを渡す。ユーザの git の設定は読まない。Operator の認証情報は使わない。

## 後片付け

fixture の workflow は、sandbox の全ての Pull Request で動く。`live-fail-on-marker` は `live/fail-marker` を変える Pull Request で失敗し、`live-check-1-required-line` は `live/check-1.md` を変えて決まった1行を持たない Pull Request で失敗し、`live-skipped-for-bots` は bot (GitHub App) の Pull Request で飛ばされ、`live-commit-status` は commit status を1つ作る。

テストは、Pull Request、ブランチ、Issue を作った直後に、後片付けを登録する。テストが途中で失敗しても、作ったものは閉じられ、消される。main に merge した小さなファイル (`live/<日時>.md`) は残る。

## 確かめる内容

| テスト | 内容 |
|---|---|
| `TestLiveSetupChecks` | [GitHub Appの登録手順](github-app-setup.md) の「確認すること」。main への push と merge の拒否、cumin-core による merge、Issue と sub-issue と blocked by、approve、表示名、保護されたパスのcheck、作成者の種類 |
| `TestLive_AgentEnvironment` (`internal/agent`) | cumin が Agent のために組み立てる環境 ([Agentの実行の設計](../designs/agent-run.md) の「Agentの環境」) で、本物の git と gh を動かす。Implementer App の token と bot の身元を `Service` と同じ手順で用意し、sandbox の worktree で `git config --show-origin` に Host のファイルが出ないこと、commit と push と `gh pr create` が通ること、Pull Request の作成者と commit の作者が Implementer App の bot であることを確かめる。Claude Code は起動せず、利用枠を使わない。Pull Request は閉じ、ブランチと worktree は消す |
| `TestLive_AgentRunOnSandbox` (`internal/agent`) | 本物の Claude Code を `Service.Start` で起動し (使用率の最小実行と Implementer の実行の 2 回、利用枠を使う)、sandbox の worktree でファイルを 1 つ commit して push し、`gh pr create` で Pull Request を開かせる。Pull Request の作成者と commit の作者が Implementer App の bot であることを確かめる。起動の記録の確認 (`init` イベント) も本物の実行で通る。Pull Request は閉じ、ブランチと worktree は消す |
| `TestLiveGitHubFacts` | cumin の実装が前提にする GitHub の事実。check run と commit status の読み取り、token の絞り込み、飛ばされた必須のcheckと merge、失敗したcheckについて読める範囲、レビューの `state` と `commit_id`、`Closes #N` で sub-issue が閉じること、GraphQL の項目、`rules/branches`、@メンション、Pull Request へのラベル |
| `TestLiveCloseReferences` | 「wait for the checks」と merge のあとの Issue の閉じ方が前提にする事実。`Closes #N` の自動のリンクに頼らない。cumin-core の App が `addCloseIssueReferences` で Implementer App の Pull Request を Issue にリンクでき、そのリンクがすぐ `closedByPullRequestsReferences` に現れること。cumin-core の App が Issue を完了として閉じられ、閉じた Issue をもう一度閉じてもエラーにならず、`closed` のイベントが増えないこと。リンクした Pull Request を merge したときに GitHub が Issue を閉じるかは、記録するだけで、合否に使わない |
| `TestLiveMergeFacts` | merge (「start the merge」) が前提にする事実。cumin-core の App で、Maintainer、Implementer の bot、協力者でない人の権限 (`permission`、`user.type`) を読めること。`sha` が先頭と違う merge は 409 になること。衝突のない Pull Request の `sha` 付きの merge が 200 になること。衝突する Pull Request の merge の返事 (405) と、そのあとの `mergeable` が `false` になること。Agent も利用枠も使わない。main に小さなファイル (`live/<日時>-conflict.md`) が1つ残る |
| `TestLiveReviewRequestFacts` | Issue Owner へのレビューの依頼 (#305) が前提にする事実。cumin-core の App が、Implementer App の Pull Request で `CUMIN_LIVE_OWNER` のレビューを依頼でき (201)、その login が `requested_reviewers` に現れること。同じ依頼をもう一度送っても失敗せず、login が1つのままであること。協力者でない人への依頼が 422 になること。Agent も利用枠も使わない。Pull Request は閉じ、ブランチは消す |
| `TestLiveDiagramsBranch` | Planner の App が書き込めるのは `cumin/diagrams` だけであること。Planner の App が `cumin/diagrams` にファイルを足せ、ほかのブランチの作成、既定のブランチの移動、タグの作成、`cumin/diagrams` の force push と削除を拒否されること。cumin-core の App も `cumin/diagrams` を削除できないこと。Implementer の App が今までどおりブランチを作って消せること。`scripts/setup-repo.sh` を `--core-app` と `--implementer-app` 付きで実行してあり、Planner の App の Contents の書き込みがインストールで承認されている必要がある。`cumin/diagrams` に `live/<日時>.svg` が1つ残る |
| `TestLiveFollowUpFixture` | 場面 Follow-1 の準備だけを行う。`cumin/type/requirement` だけの要求Issue、その sub-issue、それを閉じる Implementer の App の Pull Request を作り、Reviewer の App が `(non-blocking)` の指摘を2つ書き、Implementer の App が1つに `Fixed` で返答する。何も閉じない。Maintainer が手で merge し、場面の手順で片付ける |
| `TestLiveE2E` (`cmd/cumin`) | 場面 E2E-1 の全体。launchd で動く cumin に、1つの要求Issueを分割から受け入れの確認まで通させ、Maintainer の操作を `gh` のログインで代わりに行い、各段階を GitHub の事実と cumin のログで確かめる。利用枠を使い、1時間ほどかかる。下の「実機の場面 E2E-1」に従って実行する |

## `cumin run` を sandbox で動かすとき

定期確認や着手を sandbox で確かめるときは、`go build -o cumin ./cmd/cumin` で組み込んだバイナリを動かす。`go run` で動かすと、親のプロセスに送った SIGTERM が `cumin` の子プロセスに届かず、止め方の確認にならない (2026-09-21 に確かめた)。launchd は組み込んだバイナリを起動するので、Host の運用には関係しない。

`cumin run` は、`cumin/status/ready` の付いた sub-issue を見つけると本物の Implementer を、`cumin/status/ready` の付いた要求Issueか、sub-issue が全て閉じた `cumin/status/implementing` の要求Issueを見つけると本物の Planner を起動する。どれも利用枠を使う。新しい着手 (「request the split」、「request the implementation」) は、使用率の最小の実行が0回か1回と、Agent の実行が1回である。読んだばかり (5分以内) の使用率が状態ファイルにあれば、最小の実行はなく、`quota usage read` も出ない ([利用枠の設計](../designs/quota.md) の「着手の前の確認」)。前の Agent の実行が終わってから5分以内の着手が、これに当たる。受け入れの確認 (「request the acceptance check」) と続きの依頼 (「request a check fix」、「request a review fix」、Reviewer) は Agent の実行の1回である。下の場面に書いた起動の回数は、最小の実行を毎回行ったときの数であり、最小の実行の分だけ少なくなりうる。動かす前に確かめること。

- Host の設定ファイルに、sandbox のリポジトリと、4つの App (`cumin-core` と3つの role) の Client ID がある。秘密鍵が Keychain にある。
- `work_dir` が、捨ててよいディレクトリを指している。cumin はその下に clone と worktree を作る。
- sandbox に、`cumin/status/ready` の付いた Issue と、sub-issue が全て閉じた `cumin/status/implementing` の要求Issueが、確かめたいものだけある。ほかにあると、そちらにも着手する。
- sandbox に、同時に進めるIssueの数に数えられるIssueが残っていない。`cumin/status/planning` の要求Issueと、`cumin/status/implementing`、`cumin/status/checking`、`cumin/status/reviewing` の開いている sub-issue である。前の実行が途中で止まって残っていると、上限 (初期値は1) が埋まり、着手しない。閉じるか、ラベルを外す。

止めるときは SIGTERM を送る。動いている Agent の実行が終わるまで待つので、すぐには終わらない。実行を待たずに終わらせたいときは、もう一度 SIGTERM を送らずに、動いている role の実行の時間の上限 (`roles.implementer.time_limit` や `roles.planner.time_limit`) を短くした設定で動かし直す。

## 実機の場面 Impl-1

`cumin/status/ready` の付いた実装Issueから、Pull Request が開いて `cumin/status/checking` に移るまでを、1回通して確かめる。本物の Claude Code を最大2回起動する (使用率の最小の実行が0回か1回と、Implementer の実行) ので、利用枠を使う。

### 準備

1. `go build -o cumin ./cmd/cumin` でバイナリを作る。
2. この場面だけの設定ファイルを1つ作る。Host の設定ファイルとは別にして、対象を sandbox だけにする。`work_dir` は捨ててよい一時ディレクトリにする。`github_apps` の表は Host の設定ファイルから写す。

   ```toml
   repositories = ["<owner>/<repo>"]
   work_dir = "<捨ててよい一時ディレクトリ>"
   poll_interval = "20s"
   idle_poll_interval = "20s"

   [roles.implementer]
   time_limit = "20m"

   [github_apps.<owner>]
   cumin-core = "<Client ID>"
   planner = "<Client ID>"
   implementer = "<Client ID>"
   reviewer = "<Client ID>"
   ```

3. sandbox に要求Issueを1つ作り、`cumin/type/requirement` だけを付ける。状態ラベルは付けない。sub-issue に `cumin/status/ready` を付けると、「mark the requirement as in work」が要求Issueを `cumin/status/implementing` に替える。要求Issueに `cumin/status/ready` を付けると「request the split」が成り立ち、Planner の分割まで走る。`cumin/status/implementing` を先に付けると、sub-issue に状態ラベルがないあいだに「ask about the remaining sub-issues」が成り立ち、通知が1回余計に出る。
4. その sub-issue として実装Issueを1つ作り、`risk/low` を付ける。数分で終わる内容にする。保護されたパスを触らせない (例: `live/` の下にファイルを1つ作って1行書く)。題はブランチの名前になるので、短い英語にする。
5. sandbox に `cumin/status/ready` の付いた他の sub-issue がないことを確かめる。あると、そちらにも着手する。

### 実行

6. `./cumin run --config <設定ファイル>` を起動する。起動時のログは `skills written`、`cumin run starts`、`poll` の順に出る。足りないラベルがあれば、`cumin run starts` と `poll` の間に `created the label` が出る。Keychain に webhook のアドレスがなければ、`cumin run starts` の前に警告が1行出て、`notifications` は `none` になる ([セットアップの手順](setup-guide.md) の「通知のアドレスを Keychain に入れる」)。
7. 実装Issueに `cumin/status/ready` を付ける。
8. 次の定期確認から、ログがこの順に出る。

   | ログの行 | 意味 |
   |---|---|
   | `quota usage read` | 使用率の最小の実行が終わった。ラベルを替える前に読む (「stop agent starts」)。読んだばかり (5分以内) の使用率が状態ファイルにあれば、この行は出ない |
   | `request the implementation: claimed the issue` | ラベルを `cumin/status/implementing` に替えた |
   | `clone created`、`worktree created` | 作業場所を用意した |
   | `request the implementation: requested the work` | ブランチの名前を決めて、Implementer を起動した |
   | `agent token created`、`agent identity read` | roleのtokenとbotの身元 |
   | `agent start`、`agent end` | Claude Code の実行の始まりと終わり |
   | `the agent run ended` | 結果 (`done` か `blocked`) とセッションの番号 |
   | `wait for the checks: verified the pull request` | 検証が通り、ラベルを `cumin/status/checking` に替えた |

9. `wait for the checks: verified the pull request` が出たら、SIGTERM で止める。

### 確かめること

| # | 確かめること | 見る場所 |
|---|---|---|
| 1 | 実装Issueのラベルが `cumin/status/ready` から `cumin/status/implementing` を経て `cumin/status/checking` に移った。状態ラベルは常に1つだけ | Issue のイベント |
| 2 | Pull Request がちょうど1つ開いている。ブランチは `cumin/<Issue番号>-<短い説明>` で、`request the implementation: requested the work` の `branch` と同じ | Pull Request |
| 3 | Pull Request の本文に `Closes #<Issue番号>` があり、`pull-request.md` の見出しに従っている | Pull Request |
| 4 | Pull Request の作成者が Implementer の App の bot である。GraphQL の `author` の型が `Bot` である | GraphQL の `closedByPullRequestsReferences` |
| 5 | Pull Request の先頭のコミットが、worktree の先頭のコミットと同じである | `git -C <work_dir>/<owner>/<repo>/<Issue番号>-implementer rev-parse HEAD` と `headRefOid` |
| 6 | コミットの作者とコミッターが、Implementer の bot のユーザである | コミットの作者 |
| 7 | 起動の記録の確認が通った。異常終了「user-level context」が出ていない | cumin のログ |
| 8 | Agent に渡された skill の一覧に、cumin の3つの skill (`cumin-pull-request`、`cumin-review-reply`、`cumin-decision-request`) が載っている。Host のユーザの `~/.claude/skills/` の skill は載っていない | Claude Code のセッションの記録 (`~/.claude/projects/` の下の、worktree に対応するディレクトリ) の、"The following skills are available for use with the Skill tool" で始まる system-reminder |
| 9 | Implementer が、Pull Request を作る前に `cumin-pull-request` の skill を呼び、skill がエラーにならずに開いた | 同じ記録の `Skill` のツールの呼び出しと、その直後の `gh pr create` |
| 10 | ログに token、秘密鍵、使用率の数値が出ていない | cumin のログ |

8と9を cumin のログで確かめられない理由:

- cumin は `init` のイベントの `skills` を読むが、確かめるのは、cumin がその role のために書き出した skill が全て載っていることだけである。1つでも欠けていれば、実行は異常終了「user-level context」で止まる (7で分かる)。載っている skill の一覧そのものは、ログに残らない。
- Host のユーザの skill が載っていないことは、cumin の確認の範囲ではない。名前では組み込みの skill と区別できないためである。
- Claude Code のセッションの記録には `init` のイベントそのものが入らない。残るのは、やりとりと、文脈に入った文章 (attachment) である。
- そのかわり、Agent に実際に渡った skill の一覧は、文脈に入る system-reminder として記録に残る。組み込みの skill も同じ一覧に並ぶので、cuminの3つがあることと、Hostのユーザの skill がないことを見る。

セッションの記録には token が載りうるので、記録の全体を画面やIssueに写さない。探すのは skill の名前と呼び出しの順だけにする。

### 後片付け

- Pull Request を閉じ、そのブランチを消す。
- 実装Issueと要求Issueを閉じる。
- `work_dir` の一時ディレクトリを消す。
- sandbox に残るのは、閉じたIssueと閉じたPull Requestだけになる。

### 記録

結果は #101 にコメントとして残す。書き方は [Agentの実機の確認](agent-live-check.md) の「記録の決まり」に従う。使用率の数値、セッションの番号、手元の絶対パス、Client ID、App の名前は書かない。

## 実機の場面 Fail-1

Implementer が `blocked` を返したときに、cumin が理由をIssueに書き、ラベルを `cumin/status/awaiting-decision` に替え、Discord に通知を1件送るところを、1回通して確かめる。本物の Claude Code を最大2回起動する (使用率の最小の実行が0回か1回と、Implementer の実行) ので、利用枠を使う。

受け入れテストは偽の webhook を相手にするので、本物のメッセージが本物のチャンネルに届くことと、そのリンクが開くことは、この場面でだけ分かる。

### 準備

1. `go build -o cumin ./cmd/cumin` でバイナリを作る。
2. webhook のアドレスを Keychain に入れる。まだなら `cumin setup notify --discord-webhook` を実行する ([セットアップの手順](setup-guide.md) の「通知のアドレスを Keychain に入れる」)。
3. 場面 Impl-1 と同じ形の設定ファイルを1つ作る。対象は sandbox だけ、`work_dir` は捨ててよい一時ディレクトリにする。`notify.discord.enabled` は初期値の `true` のままにする。
4. sandbox に要求Issueを1つ作り、`cumin/type/requirement` だけを付ける (Impl-1 の手順3と同じ理由)。
5. その sub-issue として実装Issueを1つ作り、`risk/low` を付ける。Implementer が必ず `blocked` を返す内容にする。決まっていないことが1つあり、推測では進めないと分かる題と本文にする。Agent は、決まっていないことに当たったら `blocked` を返す ([Agentに共通の要件](../requirements/agents/common.md))。
6. sandbox に `cumin/status/ready` の付いた他の sub-issue がないことを確かめる。

### 実行

7. `./cumin run --config <設定ファイル>` を起動する。起動のログの `notifications` が `discord` であることを確かめる。`none` なら、手順2ができていない。
8. 実装Issueに `cumin/status/ready` を付ける。
9. 次の定期確認から、ログがこの順に出る。

   | ログの行 | 意味 |
   |---|---|
   | `request the implementation: claimed the issue` | ラベルを `cumin/status/implementing` に替えた |
   | `request the implementation: requested the work` | ブランチの名前を決めて、Implementer を起動した |
   | `the agent run ended` | 結果が `blocked` で返った |
   | `the agent returned blocked` | 理由の1行目 |
   | `stop the implementation: wrote the reason on the issue` | `blocked_reason` をコメントとして投稿した |
   | `stop the implementation: the issue waits for a Maintainer` | ラベルを `cumin/status/awaiting-decision` に替えた |
   | `the notification was sent` | Discord に送った |

10. `the notification was sent` が出たら、SIGTERM で止める。

### 確かめること

| # | 確かめること | 見る場所 |
|---|---|---|
| 1 | 実装Issueのラベルが `cumin/status/ready` から `cumin/status/implementing` を経て `cumin/status/awaiting-decision` に移った。状態ラベルは常に1つだけ | Issue のイベント |
| 2 | 実装Issueにコメントが1つ付き、`## Decision needed:` で始まる決まった形式である ([decision-request.md](../../../templates/decision-request.md)) | Issue のコメント |
| 3 | コメントの作成者が cumin本体の App の bot (`cumin-core[bot]`) である。文章はAgentが書き、投稿するのは cumin だからである | Issue のコメント |
| 4 | Pull Request が作られていない。やり直しも起きていない (Claude Code の起動は、使用率の最小の実行を除いて1回だけ) | Pull Request の一覧と、cumin のログ |
| 5 | Discord にメッセージが1件だけ届いた。1行目に `stop the implementation` と理由が入っている | Discord のチャンネル |
| 6 | そのメッセージのリンクが、2のコメントを開く | Discord のメッセージ |
| 7 | ログに token、秘密鍵、webhook のアドレス、使用率の数値が出ていない | cumin のログ |

### 後片付け

- 実装Issueと要求Issueを閉じる。
- `work_dir` の一時ディレクトリを消す。
- Discord のメッセージは残してよい。消すなら、Operator が自分で消す。

### 記録

結果は #139 にコメントとして残す。書き方は [Agentの実機の確認](agent-live-check.md) の「記録の決まり」に従う。使用率の数値、セッションの番号、手元の絶対パス、Client ID、App の名前、webhook のアドレスは書かない。

## 実機の場面 Check-1

Implementer の Pull Request で必須のcheckが1つ落ち、cumin が同じセッションで修正を1回だけ依頼し (「request a check fix」)、修正でcheckが通って、Issue が `cumin/status/reviewing` に移る (「request the review」) までを、1回通して確かめる。本物の Claude Code を最大3回起動する (使用率の最小の実行が0回か1回と、最初の依頼と修正の依頼の Implementer の実行。修正の依頼は新しい着手ではないので、使用率を読まない) ので、利用枠を使う。

受け入れテストは偽の GitHub と偽の CLI を相手にするので、本物の check の失敗の内容が依頼に載ること、`--resume` で本物のセッションが続くこと、修正の push で check が走り直すことは、この場面でだけ分かる。

### 準備

1. sandbox の fixture の workflow が、`internal/platform/github/testdata/cumin-live-fixture.yml` と同じで、`live-check-1-required-line` の job を持っている。`live-check-1-required-line` が必須のcheckに入っている (「前提」)。どちらもリポジトリの管理者が用意する。
2. `go build -o cumin ./cmd/cumin` でバイナリを作る。
3. 場面 Impl-1 と同じ形の設定ファイルを1つ作る。対象は sandbox だけ、`work_dir` は捨ててよい一時ディレクトリにする。`max_check_fix_requests` は初期値の3のままにする。
4. Host で launchd の cumin が動いていれば、設定の対象によらず止める (`launchctl bootout gui/$(id -u)/dev.cloveclove.cumin`)。`--config` で設定ファイルを分けても、状態ファイル (`~/.local/state/cumin/state.json`) は同じである。2つの cumin がそれぞれ手元の内容でファイル全体を書き直すので、ほかのリポジトリのセッションの番号と回数が消えうる。
5. sandbox に要求Issueを1つ作り、`cumin/type/requirement` だけを付ける (Impl-1 の手順3と同じ理由)。
6. その sub-issue として実装Issueを1つ作り、`risk/low` を付ける。題は `Describe the live scenario Check-1` とし、本文には「`live/check-1.md` を作り、場面 Check-1 が何を確かめるかを英語で2〜3文で書く」とだけ書く。決まった1行 (`Checked by the live scenario Check-1 in pull request #<番号>.`) は書かない。1行には Pull Request の番号が入るので、Implementer が worktree の fixture の workflow を読んでも、Pull Request を開く前の最初の push では持てない。Implementer は、落ちたcheckの内容からそれを知る。
7. sandbox に `live/check-1.md` がまだないことと、`cumin/status/ready` の付いた他の sub-issue がないことを確かめる。

### 実行

8. `./cumin run --config <設定ファイル>` を起動する。
9. 実装Issueに `cumin/status/ready` を付ける。
10. 次の定期確認から、ログがこの順に出る。「copy the labels to the pull request」(`copy the labels to the pull request: copied the labels of the issue to the pull request`) は、ラベルが替わったあとの定期確認ごとに間に入る。

   | ログの行 | 意味 |
   |---|---|
   | `request the implementation: claimed the issue` | ラベルを `cumin/status/implementing` に替えた |
   | `request the implementation: requested the work` | `kind` が `implement`。Implementer を新しいセッションで起動した |
   | `the agent run ended` | 1回目の実行が `done` で終わった |
   | `wait for the checks: verified the pull request` | ラベルを `cumin/status/checking` に替えた |
   | `poll` | `required_checks` が1以上。check が終わるまで、何も起きない定期確認が続く |
   | `request a check fix: a required check failed; the issue goes back to the Implementer` | `failed` に `live-check-1-required-line`、`check_fix_requests` が1 |
   | `request a check fix: requested the work` | `kind` が `check fix`、`resumed` が `true` |
   | `the agent run ended` | 修正の実行が `done` で終わった |
   | `wait for the checks: verified the pull request` | ラベルが `cumin/status/checking` に戻った |
   | `request the review: the pull request is ready for review` | 必須のcheckが全て通り、ラベルを `cumin/status/reviewing` に替えた |

11. 1回目の実行のあとの先頭のコミットで `live-check-1-required-line` が通ってしまったら (Implementer が Pull Request の番号を知ったあとで1行を足して push し直したとき)、「request a check fix」は起きずに「request the review」に進む。その回は数えずに、後片付けをしてからやり直す。Implementer は check を待たない約束なので、ふつうは起きない。
12. `request the review: the pull request is ready for review` のあと、もう1回定期確認が回って「copy the labels to the pull request」が Pull Request のラベルを替えたら、SIGTERM で止める。

### 確かめること

| # | 確かめること | 見る場所 |
|---|---|---|
| 1 | 実装Issueのラベルが `ready`、`implementing`、`checking`、`implementing`、`checking`、`reviewing` の順に移った。状態ラベルは常に1つだけ | Issue のイベント |
| 2 | Pull Request がちょうど1つ開いている。2回の実行が同じブランチに積んだ | Pull Request とそのコミット |
| 3 | 1回目の先頭のコミットで `live-check-1-required-line` が落ち、修正のあとの先頭のコミットで通った。ほかの必須のcheckは通ったか飛ばされた | Pull Request の check |
| 4 | 修正の依頼がちょうど1回である。`request a check fix: requested the work` が1行だけで、Claude Code の起動は、使用率の最小の実行を除いて2回である | cumin のログ |
| 5 | 修正の依頼が、1回目の実行のセッションを再開した。2回の `the agent run ended` のセッションの番号が同じである。公式文書が新しい番号を与えると書くのは `--fork-session` と `/branch` だけなので、再開で番号が変わらないことはこの場面で確かめる | cumin のログ (番号は記録に書かない) |
| 6 | 修正の依頼文に、落ちたcheckの名前と、annotation の文 (決まった1行を含む) が載っていた | Claude Code のセッションの記録で、`Request: check fix` で始まるユーザの入力 |
| 7 | 修正のあとの `live/check-1.md` に、Pull Request の番号の入った決まった1行がある。最初のコミットにはない | Pull Request のコミットごとの差分 |
| 8 | Pull Request のラベルが、最後に `cumin/status/reviewing` と `risk/low` である (「copy the labels to the pull request」) | Pull Request |
| 9 | ログに token、秘密鍵、使用率の数値が出ていない | cumin のログ |

セッションの記録には token が載りうるので、記録の全体を画面やIssueに写さない。6で見るのは、依頼文の見出しと、checkの名前と、annotation の1文だけにする。

### 後片付け

- Pull Request を閉じ、そのブランチを消す。`live/check-1.md` は main に入らない。
- 実装Issueと要求Issueを閉じる。
- `work_dir` の一時ディレクトリを消す。
- 手順4で launchd の cumin を止めたなら、`launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/dev.cloveclove.cumin.plist` で戻す。
- Host の状態ファイルに、実装Issueのセッションの番号と回数が1件残る。sandbox の閉じたIssueのものなので、そのままでよい。

### 記録

結果は #185 にコメントとして残す。書き方は [Agentの実機の確認](agent-live-check.md) の「記録の決まり」に従う。使用率の数値、セッションの番号、手元の絶対パス、Client ID、App の名前は書かない。

## 実機の場面 Plan-1

`cumin/status/ready` の付いた要求Issueを、Planner が sub-issue に分割し、要求Issueが `cumin/status/awaiting-plan-review` に移るまでを、1回通して確かめる (「request the split」、「ask for the plan review」)。本物の Claude Code を最大2回起動する (使用率の最小の実行が0回か1回と、Planner の実行) ので、利用枠を使う。

### 準備

1. `go build -o cumin ./cmd/cumin` でバイナリを作る。
2. この場面だけの設定ファイルを1つ作る。Impl-1 の手順2と同じ形で、`[roles.implementer]` の代わりに `[roles.planner]` の `time_limit = "30m"` を書く。
3. sandbox にマイルストーンを1つ作る。Planner が、要求Issueのマイルストーンを sub-issue に付けることを確かめるためである。
4. sandbox に要求Issueを1つ作り、`cumin/type/requirement` とそのマイルストーンを付ける。`cumin/status/ready` はまだ付けない。本文には Requirements を3つ書く。2つか3つの sub-issue に分かれ、1つが他の1つを待つ内容にする (例: `live/` の下にページを2つ作り、ルートの `README.md` からその1つにリンクする)。保護されたパスを触らせない。
5. sandbox に、`cumin/status/ready` の付いた他の Issue がないことを確かめる。

### 実行

6. `./cumin run --config <設定ファイル>` を起動する。
7. 要求Issueに `cumin/status/ready` を付ける。
8. 次の定期確認から、ログがこの順に出る。

   | ログの行 | 意味 |
   |---|---|
   | `quota usage read` | 使用率の最小の実行が終わった。ラベルを替える前に読む (「stop agent starts」)。読んだばかり (5分以内) の使用率が状態ファイルにあれば、この行は出ない |
   | `request the split: moved the requirement issue to planning` | ラベルを `cumin/status/planning` に替えた |
   | `clone created`、`worktree created` | 既定のブランチを detached で開いた |
   | `request the split: requested the Planner` (`kind` が `plan`) | Planner を起動した |
   | `agent token created`、`agent identity read` | roleのtokenとbotの身元 |
   | `agent start`、`agent end`、`the agent run ended` | Claude Code の実行と、その結果 |
   | `ask for the plan review: the split waits for a Maintainer` (`sub_issues` が sub-issue の数) | 検証が通り、ラベルを `cumin/status/awaiting-plan-review` に替えた |
   | `the notification was sent` (`action` が `ask for the plan review`) | 通知した。Keychain に webhook のアドレスがなければ、代わりに通知がないことの警告が出る |

9. `ask for the plan review: the split waits for a Maintainer` のあとの通知の行 (`the notification was sent`、または通知できなかったことのログ) が出たら、Accept-1 に進むか、SIGTERM で止める。通知はラベルを替えたあとに出すので、`ask for the plan review: ...` の行で止めると、通知が取り消されることがある。

### 確かめること

| # | 確かめること | 見る場所 |
|---|---|---|
| 1 | 要求Issueのラベルが `cumin/status/ready` から `cumin/status/planning` を経て `cumin/status/awaiting-plan-review` に移った。状態ラベルは常に1つだけ | Issue のイベント |
| 2 | sub-issue が2つか3つあり、要求Issueの sub-issue になっている。作成者は Planner の App である | 要求Issueの sub-issue の一覧 |
| 3 | 全ての sub-issue に `risk/*` がちょうど1つと、要求Issueのマイルストーンが付いている | 各 sub-issue |
| 4 | 待つ必要のある sub-issue に blocked by がある | 各 sub-issue の依存関係 |
| 5 | 要求Issueに、Planner の App のコメントがちょうど1つある。`plan-summary.md` の見出しに従っている | 要求Issueのコメント |
| 6 | 起動の記録の確認が通った。異常終了が出ていない | cumin のログ |
| 7 | Agent に渡された skill の一覧に、Planner の4つの skill (`cumin-implementation-issue`、`cumin-plan-summary`、`cumin-acceptance-check`、`cumin-decision-request`) がある。他の role の skill と、Host のユーザの `~/.claude/skills/` の skill はない。Planner は sub-issue を作る前に `cumin-implementation-issue` を、コメントの前に `cumin-plan-summary` を呼んだ | Claude Code のセッションの記録 (Impl-1 の8と同じ見方)。worktree のディレクトリは `<Issue番号>-planner` である |
| 8 | ログに token、秘密鍵、使用率の数値が出ていない | cumin のログ |
| 9 | どの sub-issue も「Where this fits」に英語の図の画像があり、対象の箇所が色で分かる。画像は `cumin/diagrams` の `issue-<要求Issueの番号>/` にあるSVGで、コミットを指定している。`cumin/diagrams` のほかに、Planner の App が作ったブランチやタグがない | 各 sub-issue、sandbox の `cumin/diagrams`、ブランチとタグの一覧 |

skill の一覧を探すときは、"The following skills are available for use with the Skill tool" で始まる部分だけを読む。記録の全体を名前で grep すると、Planner が読んだファイルの中身に当たることがある。

### 後片付け

- sub-issue と要求Issueを、not planned で閉じる。
- マイルストーンを閉じる。
- Accept-1 に進まないなら、`work_dir` の一時ディレクトリを消す。

### 記録

結果は、この場面を作った Issue (#172) にコメントとして残す。書き方は [Agentの実機の確認](agent-live-check.md) の「記録の決まり」に従う。使用率の数値、セッションの番号、手元の絶対パス、Client ID、Organization と App の名前は書かない。

## 実機の場面 Accept-1

sub-issue が全て閉じた要求Issueに、Planner が受け入れの確認のコメントを書き、要求Issueが `cumin/status/awaiting-acceptance` に移るまでを、1回通して確かめる (「request the acceptance check」、「ask for the acceptance」)。本物の Claude Code を1回起動するので、利用枠を使う。受け入れの確認の依頼は新しい着手ではないので、使用率を読む最小の実行はしない。Plan-1 と同じ cumin の実行の中で続けてよい。

### 準備

1. Plan-1 の手順1と2と同じバイナリと設定ファイルを使う。
2. sandbox に要求Issueを1つ作り、`cumin/type/requirement` だけを付ける。本文の Requirements には、sandbox の main で既に成り立つことを2つ書く (例: ルートの `README.md` の1行目の見出し)。確認の結果が Pass になり、Planner が根拠を書ける。
3. その sub-issue を2つ作り、`risk/low` を付けて、completed で閉じる。
4. 最後に、要求Issueに `cumin/status/implementing` を付ける。sub-issue が開いているうちに付けると「ask about the remaining sub-issues」が成り立ち、要求Issueが `cumin/status/awaiting-plan-review` に移ってしまう。

### 実行

5. cumin が動いていなければ、`./cumin run --config <設定ファイル>` を起動する。次の定期確認から、ログがこの順に出る。

   | ログの行 | 意味 |
   |---|---|
   | `request the acceptance check: read the comments` | sub-issue が全て閉じた要求Issueのコメントを読んだ。確認が終わるまで、定期確認のたびに出る |
   | `worktree removed`、`worktree created` | 前の依頼の worktree を消し、merge された main で開き直した。初めての要求Issueでは `worktree removed` は出ない |
   | `request the acceptance check: moved the requirement issue to accepting` | ラベルを `cumin/status/accepting` に替えた |
   | `request the acceptance check: requested the Planner` (`kind` が `acceptance check`) | Planner を起動した |
   | `agent token created`、`agent start`、`agent end`、`the agent run ended` | Plan-1 と同じ。使用率を読む `quota usage read` は出ない |
   | `ask for the acceptance: the requirement issue waits for the acceptance of a Maintainer` | 実行の終わり (または次の定期確認) で確認のコメントを見つけ、ラベルを `cumin/status/awaiting-acceptance` に替えた |
   | `the notification was sent` (`action` が `ask for the acceptance`) | 通知した |

   Planner が確認のコメントを書いてからプロセスが終わるまでの間に定期確認が入ると、`ask for the acceptance: ...` と通知の行が、`agent end` と `the agent run ended` より先に出る。どちらの順でもよい。境目は、ログの順ではなく、確認のコメントが書かれた時刻である。

6. `ask for the acceptance: ...` のあとの通知の行 (Plan-1 の手順9と同じ) が出たら、cumin のプロセスに SIGTERM を送る。`stopped` の行が出て、終了コード0で終わる。止めるときに途中だった定期確認は、`poll failed` (`context canceled`) を1行出すことがある。止めた結果で、問題ではない。

### 確かめること

| # | 確かめること | 見る場所 |
|---|---|---|
| 1 | 要求Issueのラベルが、依頼の前に `cumin/status/accepting` に移り、確認のコメントのあとに `cumin/status/awaiting-acceptance` に移った | Issue のイベント |
| 2 | 要求Issueに、`## Acceptance check` で始まる Planner の App のコメントがちょうど1つある。Requirements の1項目ごとに1行あり、どの行にも根拠がある | 要求Issueのコメント |
| 3 | 実行で Issue が作られず、sub-issue も変わらなかった | sandbox の Issue の一覧と、sub-issue の更新の時刻 |
| 4 | Planner は、コメントの前に `cumin-acceptance-check` を呼んだ。skill の一覧は Plan-1 の7と同じ | Claude Code のセッションの記録 |
| 5 | ログに token、秘密鍵、使用率の数値が出ていない | cumin のログ |

### 後片付け

- 要求Issueを completed で閉じる。
- `work_dir` の一時ディレクトリを消す。

### 記録

Plan-1 と同じ Issue に、同じ決まりで残す。

## 実機の場面 Review-1

必須のcheckが通った Pull Request を、cumin が Reviewer に渡し (「request the review」)、Reviewer が先頭のコミットに `APPROVE` を出すまでを、1回通して確かめる。本物の Claude Code を最大4回起動する (使用率の最小の実行と Agent の実行を、Implementer と Reviewer で1組ずつ)。Reviewer の1ラウンド目は組み込みのレビューの skill も動かすので、利用枠を多めに使う。

受け入れテストは偽の CLI がレビューを出すので、本物の Reviewer が `commit_id` を付けて先頭のコミットにレビューを出すこと、組み込みのレビューの skill を headless の実行で呼べることは、この場面でだけ分かる。

### 準備

1. `go build -o cumin ./cmd/cumin` でバイナリを作る。
2. 場面 Impl-1 と同じ形の設定ファイルを1つ作る。対象は sandbox だけ、`work_dir` は捨ててよい一時ディレクトリにする。`[roles.reviewer]` の `time_limit` を `"30m"` にする。`max_review_rounds` は初期値の3のままにする。
3. Host で launchd の cumin が動いていれば止める (場面 Check-1 の手順4と同じ理由)。
4. sandbox に要求Issueを1つ作り、`cumin/type/requirement` だけを付ける (場面 Impl-1 の手順3と同じ理由)。
5. その sub-issue として実装Issueを1つ作り、`risk/low` を付ける。題は `Describe the live scenario Review-1` とし、本文の完了条件には「`live/review-1.md` を作り、場面 Review-1 が何を確かめるかを英語で2〜3文で書く」とだけ書く。
6. sandbox に `live/review-1.md` がまだないことと、`cumin/status/ready` の付いた他の sub-issue がないことを確かめる。同時に進めるIssueの数に数えられるIssue (`cumin/status/reviewing` を含む) が残っていないことも確かめる (「`cumin run` を sandbox で動かすとき」)。

### 実行

7. `./cumin run --config <設定ファイル>` を起動する。
8. 実装Issueに `cumin/status/ready` を付ける。
9. 次の定期確認から、ログがこの順に出る。「copy the labels to the pull request」のログは間に入る。

   | ログの行 | 意味 |
   |---|---|
   | `request the implementation: requested the work` | Implementer を新しいセッションで起動した |
   | `wait for the checks: verified the pull request` | ラベルを `cumin/status/checking` に替えた |
   | `request the review: the pull request is ready for review` | 必須のcheckが全て通り、ラベルを `cumin/status/reviewing` に替えた。`round` が1 |
   | `request the review: requested the review` | `round` が1、`resumed` が `false` |
   | `the agent run ended` | Reviewer の実行が `done` で終わった |
   | `ask for the merge decision: the Reviewer approved the head commit` | ラベルは `cumin/status/reviewing` のまま |

10. `ask for the merge decision: the Reviewer approved the head commit` のあと、もう1回定期確認が回ったら、SIGTERM で止める。

### 確かめること

| # | 確かめること | 見る場所 |
|---|---|---|
| 1 | 実装Issueのラベルが `ready`、`implementing`、`checking`、`reviewing` の順に移り、`reviewing` のまま残った | Issue のイベント |
| 2 | Pull Request に、Reviewer の App の bot のレビューがちょうど1つあり、結果が `APPROVED` で、対象のコミットが Pull Request の先頭のコミットである | `gh api repos/<owner>/<repo>/pulls/<番号>/reviews` の `user.login`、`state`、`commit_id` |
| 3 | レビューの本文が `review.md` の形に従い、`Result: Approved (round 1 of 3)` と、実行したレビューの skill の行がある | レビューの本文 |
| 4 | Reviewer は `<Issue番号>-reviewer` の worktree で動き、その HEAD は detached で、Pull Request の先頭のコミットだった | `git -C <work_dir>/<owner>/<repo>/<Issue番号>-reviewer rev-parse HEAD` と `git ... status` |
| 5 | Reviewer に渡された skill の一覧に、`cumin-review` と `cumin-decision-request` があり、Implementer と Planner の skill はない。組み込みの `code-review` と `security-review` が一覧に載っているかを記録する | Claude Code のセッションの記録 (場面 Impl-1 の8と同じ見方)。worktree のディレクトリは `<Issue番号>-reviewer` である |
| 6 | Reviewer が、レビューを出す前に `cumin-review` を呼んだ。組み込みの `code-review` と `security-review` を呼んだか、呼べなかったか (呼べなかったなら、そのときの応答の1文) を記録する。`--comment`、`--fix`、`ultra` を付けていない | 同じ記録の `Skill` のツールの呼び出し |
| 7 | Reviewer は、コミットも push もしていない。Pull Request のコミットは Implementer のものだけである | Pull Request のコミット |
| 8 | ログに token、秘密鍵、使用率の数値が出ていない | cumin のログ |

5と6の結果は、組み込みのレビューの skill を1ラウンド目で使う決まり (Reviewerの要件の「ラウンドごとに見る範囲」) が、headless の実行で実際に効くかの記録である。呼べなかったときは、Reviewer は同じ観点を自分で確かめて続ける決まりなので、場面は失敗にしない。記録を #157 に残す。

### 後片付け

- Pull Request を閉じ、そのブランチを消す。
- 実装Issueと要求Issueを閉じる。`cumin/status/reviewing` のまま残すと、同時に進めるIssueの数を1つ使い続け、次の場面に着手しない。
- `work_dir` の一時ディレクトリを消す。
- 手順3で launchd の cumin を止めたなら、戻す。

### GitHub が紐づけを作らないとき

2026-09-30 には、GitHub が `Closes #<番号>` の紐づけを作らなかった (#157 の decision request)。そのときは、1回目の実行の終わりが、「stop the implementation」の「Issue を閉じる開いている Pull Request がない」で止まる。Maintainer が Pull Request のサイドバーの Development で実装Issueを紐づけると、API にすぐ現れる。紐づけたら、実装Issueに `cumin/status/ready` を付け直す。「request the implementation」が続きの依頼 (新しいセッション) で進める。Claude Code の起動が2回増える。記録には、手で紐づけたことを書く。

### 記録

結果は #229 にコメントとして残す。書き方は [Agentの実機の確認](agent-live-check.md) の「記録の決まり」に従う。使用率の数値、セッションの番号、手元の絶対パス、Client ID、App の名前は書かない。

## 実機の場面 Review-2

Reviewer が1ラウンド目に `REQUEST_CHANGES` を出し、cumin が Implementer に同じセッションで修正を依頼し (「request a review fix」)、Implementer が返答のテンプレートで答えて直し、2ラウンド目の Reviewer が同じセッションで `APPROVE` を出すまでを、1回通して確かめる。本物の Claude Code を最大8回起動する (使用率の最小の実行と Agent の実行を、Implementer の実装、Reviewer の1ラウンド目、Implementer の修正、Reviewer の2ラウンド目で1組ずつ)。

修正を求める指摘を確実に起こすため、Implementer の Pull Request に、完了条件を1つ破るコミットを人が足してから、レビューに進める。

### 準備

1. 場面 Review-1 の手順1〜3と同じ。場面 Review-1 の実装Issueが閉じていることを確かめる。
2. sandbox に要求Issueを1つ作り、`cumin/type/requirement` だけを付ける。
3. その sub-issue として実装Issueを1つ作り、`risk/low` を付ける。題は `Describe the live scenario Review-2` とし、本文の完了条件に次の2つを書く。
   - `live/review-2.md` を作り、場面 Review-2 が何を確かめるかを英語で2〜3文で書く。
   - `live/review-2.md` のどの行も80文字以内である。
4. sandbox に `live/review-2.md` がまだないことを確かめる。

### 実行

5. `./cumin run --config <設定ファイル>` を起動し、実装Issueに `cumin/status/ready` を付ける。
6. `wait for the checks: verified the pull request` が出たら、すぐに SIGTERM で止める。必須のcheckが通る前に止めれば、「request the review」はまだ起きていない。ログの行を1秒ごとに見て SIGTERM を送る小さなループを使うと、確実に間に合う。GitHub が紐づけを作らないときは、その前に場面 Review-1 の「GitHub が紐づけを作らないとき」の手順が入る。止める前に `request the review: the pull request is ready for review` が出てしまったら、その回は数えずに、後片付けをしてからやり直す。
7. Pull Request のブランチの `live/review-2.md` の最後に、120文字を超える英語の1行を足すコミットを作って push する。`gh api -X PUT repos/<owner>/<repo>/contents/live/review-2.md` にブランチと元のファイルの `sha` を渡せば、手元にブランチを取らずにできる。コミットの作者は Maintainer のままでよい。先頭のコミットが変わるので、必須のcheckが走り直す。
8. `./cumin run --config <設定ファイル>` を起動し直す。`cumin/status/checking` のIssueは、再起動のあとも「request the review」と「request a check fix」で続きから進む。
9. 次の定期確認から、ログがこの順に出る。

   | ログの行 | 意味 |
   |---|---|
   | `request the review: requested the review` | `round` が1、`resumed` が `false` |
   | `request a review fix: the Reviewer requested changes; the issue goes back to the Implementer` | `round` が1、`limit` が3。ラベルを `cumin/status/implementing` に替えた |
   | `request a review fix: requested the work` | `kind` が `review fix`、`resumed` が `true` |
   | `wait for the checks: verified the pull request` | 修正が push され、ラベルが `cumin/status/checking` に戻った |
   | `request the review: requested the review` | `round` が2、`resumed` が `true` |
   | `ask for the merge decision: the Reviewer approved the head commit` | 2ラウンド目で承認された |

10. `ask for the merge decision: the Reviewer approved the head commit` のあと、もう1回定期確認が回ったら、SIGTERM で止める。

### 確かめること

| # | 確かめること | 見る場所 |
|---|---|---|
| 1 | 実装Issueのラベルが `ready`、`implementing`、`checking`、`reviewing`、`implementing`、`checking`、`reviewing` の順に移った | Issue のイベント |
| 2 | Reviewer の1つめのレビューが、手順7のコミットに対する `CHANGES_REQUESTED` である。80文字の完了条件を `(blocking)` の指摘にし、`Why` と `Fix` がある | `gh api repos/<owner>/<repo>/pulls/<番号>/reviews` と、そのレビューのコメント |
| 3 | Implementer の修正が、手順7の行を直すコミットとして同じブランチに積まれた。新しい Pull Request はない | Pull Request のコミット |
| 4 | Implementer が、修正を求める指摘のスレッドに `Fixed in <SHA>.` で始まる返答を書いた (`review-reply.md`)。スレッドは解決済みにしていない。返答はレビューの API で書くので、Implementer の App の `COMMENTED` のレビューとしても現れる。cumin は Reviewer の App のレビューだけを数えるので、ラウンドは変わらない | Pull Request のレビューのスレッド |
| 5 | Implementer の2回の実行が同じセッションである (`request the implementation: requested the work` の実行と、`request a review fix: requested the work` の実行の `the agent run ended` のセッションの番号が同じ) | cumin のログ (番号は記録に書かない) |
| 6 | Reviewer の2回の実行が同じセッションで、Implementer のセッションとは違う | cumin のログ |
| 7 | 2ラウンド目の依頼文に `Round: 2 of 3` と `Last reviewed commit:` (手順7のコミット) がある。2ラウンド目の Reviewer は、組み込みのレビューの skill を呼んでいない | Claude Code のセッションの記録で、`Request: review` で始まる2つめのユーザの入力と、そのあとの `Skill` の呼び出し |
| 8 | Reviewer の2つめのレビューが、修正のあとの先頭のコミットに対する `APPROVED` で、`round 2 of 3` とある | レビュー |
| 9 | ログに token、秘密鍵、使用率の数値が出ていない | cumin のログ |

### 後片付け

- Pull Request を閉じ、そのブランチを消す。
- 実装Issueと要求Issueを閉じる。
- `work_dir` の一時ディレクトリを消す。
- launchd の cumin を止めたなら、戻す。
- Host の状態ファイルに、実装Issueの2つのセッションの番号が1件残る。sandbox の閉じたIssueのものなので、そのままでよい。

### 記録

結果は #229 にコメントとして残す。書き方は場面 Review-1 と同じである。

## 実機の場面 Merge-1

Reviewer が承認した `risk/low` の Pull Request を cumin-core が merge して実装Issueが閉じること (「start the merge」)、`risk/medium` の Pull Request は merge せずに1回だけ通知すること (「ask for the merge decision」)、Maintainer が先頭のコミットをレビューで承認すると cumin-core が merge すること (「start the merge」) を、1回通して確かめる。本物の Claude Code を最大6回起動する (2つの実装Issueごとに、着手 (「request the implementation」) の使用率の最小の実行が0回か1回、Implementer の実行、Reviewer の実行)。Reviewer への依頼は着手ではないので、使用率を読み直さない。途中で Maintainer が GitHub でレビューを1つ出す。

受け入れテストは偽の GitHub で merge するので、本物の ruleset のもとで cumin-core の merge が通ること、GitHub がリンクした実装Issueを閉じるかどうかと cumin の閉じ方、Maintainer のレビューを権限で見分けることは、この場面でだけ分かる。

### 準備

1. 場面 Review-1 の手順1〜3と同じ。ほかに、場面 Fail-1 の手順2と同じく webhook のアドレスを Keychain に入れ、`notify.discord.enabled` を `true` のままにする。
2. sandbox に要求Issueを1つ作り、`cumin/type/requirement` だけを付ける。
3. その sub-issue として、実装Issueを2つ作る。この順に作り、B の番号を小さくする。番号の小さい B から進むので、Maintainer の作業 (手順8) が早く来る。B が `cumin/status/awaiting-merge-decision` で待つ間は、同時に進めるIssueの数に数えないので、A がその間に進む。どちらも本文の完了条件には「`live/<ファイル>` を作り、場面 Merge-1 が何を確かめるかを英語で2〜3文で書く」とだけ書く。
   - B: 題は `Describe the merge of risk/medium in Merge-1`、ファイルは `live/merge-1-<日時>-medium.md`、`risk/medium` を付ける。
   - A: 題は `Describe the merge of risk/low in Merge-1`、ファイルは `live/merge-1-<日時>-low.md`、`risk/low` を付ける。
   - `<日時>` は `20261001-1635` の形の、この実行の日時である。前の実行のファイルは main に残るので、実行ごとに名前を変える。
4. sandbox に2つのファイルがまだないことと、`cumin/status/ready` の付いた他の sub-issue がなく、同時に進めるIssueの数に数えられるIssueもないことを確かめる (「`cumin run` を sandbox で動かすとき」)。

### 実行

5. `./cumin run --config <設定ファイル>` を起動し、ログをファイルにも書く。起動のログの `notifications` が `discord` であることを確かめる。同時に、2つの実装Issueが両方閉じたら SIGTERM を送る小さなループを動かす (3秒ごとに `gh api repos/<owner>/<repo>/issues/<番号> --jq .state` を読む)。両方閉じると、次の定期確認で受け入れの確認 (「request the acceptance check」) が Planner を起動する。この場面では要らないので、その前に止める。
6. 2つの実装Issueに `cumin/status/ready` を付ける。
7. B のログがこの順に出る。「copy the labels to the pull request」と「wait for the checks」のログは間に入る。

   | ログの行 | 意味 |
   |---|---|
   | `ask for the merge decision: the Reviewer approved the head commit` | Reviewer が先頭のコミットを承認した |
   | `ask for the merge decision: the merge waits for a Maintainer` | ラベルを `cumin/status/awaiting-merge-decision` に替えた |
   | `ask for the merge decision: requested the review of the Issue Owner` | `reviewer` が、実装Issueに最新の `cumin/status/ready` を付けたMaintainer (Issue Owner) のログイン名。GitHubの「レビューの依頼」の一覧にPull Requestが載る |
   | `the notification was sent` | `action` が `ask for the merge decision`。Discord に1件届く |

8. Maintainer が B の Pull Request を開き、GitHub のレビューで承認 (Approve) する。Reviewers に Maintainer を足す必要はない。
9. その間に A が進み、ログがこの順に出る。

   | ログの行 | 意味 |
   |---|---|
   | `start the merge: the Reviewer approved the head commit`、次の定期確認で `merged the pull request` | `merge_method` が `squash` |
   | GitHubが閉じなかったときは、次の定期確認で `close the merged issue: closed the issue that GitHub left open after the merge` | 実装Issueが閉じた。どちらだったかを記録する |

10. Maintainer の承認のあとの定期確認で、B のログがこの順に出る。両方閉じると、手順5のループが cumin を止める。

   | ログの行 | 意味 |
   |---|---|
   | `start the merge: a Maintainer approved the head commit` | Maintainer の権限を読み、最新の判断のレビューが承認だった |
   | 次の定期確認で `merged the pull request` | `merge_method` が `squash` |
   | GitHubが閉じなかったときは、次の定期確認で `close the merged issue: closed the issue that GitHub left open after the merge` | 実装Issueが閉じた。どちらだったかを記録する |

### 確かめること

| # | 確かめること | 見る場所 |
|---|---|---|
| 1 | A の Pull Request を merge したのが cumin-core の App で、merge の方法が squash である。A の実装Issueが閉じていて、理由が `completed` である | Pull Request の `merged_by`、main のコミット、Issue の `state_reason` |
| 2 | A の実装Issueを閉じたのが、GitHub (Pull Request の merge) か cumin-core か | Issue のイベントの `closed` の `actor` と `commit_id` |
| 3 | B の実装Issueのラベルが `reviewing` から `awaiting-merge-decision` に移り、Discord の通知が1件だけ届いた。通知に Pull Request のリンクがある | Issue のイベント、Discord |
| 4 | B は、Maintainer が承認するまで merge されなかった | Pull Request の `merged_at` と、Maintainer のレビューの時刻 |
| 5 | B の Pull Request を merge したのが cumin-core の App で、B の実装Issueが閉じた | 1と2と同じ |
| 6 | 受け入れの確認 (「request the acceptance check」) は動いていない。Planner の実行がない | cumin のログ |
| 7 | ログに token、秘密鍵、使用率の数値が出ていない | cumin のログ |

### 後片付け

- 要求Issueを閉じる。2つのファイルは main に残る。名前に日時があるので、次の実行の邪魔にならない。
- 途中で止まったときは、開いたままの実装Issueと Pull Request を閉じ、そのブランチを消す。`cumin/status/implementing`、`cumin/status/checking`、`cumin/status/reviewing` のまま残すと、同時に進めるIssueの数を使い続け、次の実行が着手しない。
- `work_dir` の一時ディレクトリを消す。
- launchd の cumin を止めたなら、戻す。

### 記録

結果は #290 にコメントとして残す。書き方は場面 Review-1 と同じである。

## 実機の場面 Follow-1

Maintainer が merge した Pull Request の残りの作業が、フォローアップノートとして要求Issueに1つ付き、cumin を再起動しても増えないことを確かめる (「write the follow-up note」、Core-10)。Claude Code は起動しないので、利用枠を使わない。要求Issueに状態ラベルを付けないので、cumin は分割 (「request the split」)、着手 (「mark the requirement as in work」)、受け入れの確認 (「request the acceptance check」) のどれも行わない。

### 準備

1. `go build -o cumin ./cmd/cumin` でバイナリを作る。設定ファイルは Impl-1 の手順2と同じ形でよい。`work_dir` は捨ててよいディレクトリにする。
2. sandbox に、`cumin/status/ready` の付いた Issue と、sub-issue が全て閉じた `cumin/status/implementing` の要求Issueがないことを確かめる。あると、そちらに Agent を起動して利用枠を使う。
3. 場面の準備を作る。Maintainer のターミナルで実行する。

   ```sh
   CUMIN_LIVE=1 CUMIN_LIVE_REPO=<owner>/<repo> go test -count=1 -run TestLiveFollowUpFixture -v ./internal/platform/github/
   ```

   最後に `Follow-1 is ready: requirement issue #<A>, sub-issue #<B>, pull request #<C>` が出る。作られるものと、作る App は次のとおりである。

   | もの | 作る App | 内容 |
   |---|---|---|
   | 要求Issue #A | Planner | `cumin/type/requirement` だけ |
   | sub-issue #B | Planner | #A の sub-issue |
   | Pull Request #C | Implementer | `live/<日時>-follow-1.md` を足す。説明の `Follow-up` に1行、`Closes #B` |
   | 指摘2つ | Reviewer | 1行目に `suggestion (non-blocking)`、2行目に `nitpick (non-blocking)` |
   | 返答1つ | Implementer | `nitpick` に `Fixed` で始まる返答 |

### 実行

4. Maintainer が、Pull Request #C を squash で merge し、ブランチを消す。必須のレビューがないので、管理者として merge する。#B が閉じる。閉じなければ、Maintainer が #B を手で閉じる。「write the follow-up note」は、誰が閉じたかを見ない。
   - 手順3の出力に `GitHub made no closing link` があれば、merge の前に、Maintainer が #C を #B に手で結び付ける (Pull Request の画面の Development)。2026-09-30 から、GitHub は新しい Pull Request の `Closes #N` を結び付けていない。結び付けないと、merge しても #B は閉じず、ノートも付かない。
5. `./cumin run --config <設定ファイル>` を起動する。最初の定期確認で、ログに `write the follow-up note: wrote the follow-up note` (`requirement_issue` が #A、`issue` が #B、`pull_request` が #C) が1行出る。
6. 次の定期確認のログ (`poll`) が出たら、SIGTERM で止める。`stopped` の行が出る。
7. もう一度 `./cumin run --config <設定ファイル>` を起動し、定期確認のログが2回出たら、SIGTERM で止める。`write the follow-up note: wrote the follow-up note` は出ない。

### 確かめること

| # | 確かめること | 見る場所 |
|---|---|---|
| 1 | #A に、cumin-core の App のコメントがちょうど1つある。1行目が `## Follow-up from #C (<#B の題>)` である | #A のコメント |
| 2 | コメントの見出し「From the pull request description」の下に、#C の `Follow-up` の1行がそのままある | #A のコメント |
| 3 | 見出し「Open non-blocking review comments」の下に、`suggestion` の指摘だけが1行あり、`<ファイル>:1` とリンクが付いている。`Fixed` の返答が付いた `nitpick` はない | #A のコメント |
| 4 | コメントの最後に、目に見えない目印 `<!-- cumin:follow-up-note issue=B pull-request=C notes=C -->` がある | #A のコメントを編集画面か API で読む |
| 5 | 手順7のあとも、#A のフォローアップノートは1つのままである | #A のコメント |
| 6 | Agent が起動していない (`agent start` の行がない)。ログに token と秘密鍵が出ていない | cumin のログ |

### 後片付け

- 要求Issue #A を not planned で閉じる。#B は、merge か手順4で閉じている。
- main に merge した `live/<日時>-follow-1.md` は残してよい。
- `work_dir` の一時ディレクトリを消す。

### 記録

結果は、この場面の Issue (#259) にコメントとして残す。書き方は [Agentの実機の確認](agent-live-check.md) の「記録の決まり」に従う。

## 実機の場面 Quota-1

本物の Claude Code の使用率で、cumin が着手の前に止まり、1回だけ通知し、`cumin quota allow` で再開するところを、1回通して確かめる (「stop agent starts」、「resume agent starts」、Core-6、Core-16)。前半は weekly 枠、後半は 5h 枠で止める。しきい値を今の使用率より低くして、止まる場面を作る。本物の Claude Code を、最小の実行で2回か3回と、後半の Implementer の実行で1回起動するので、利用枠を使う。

### 準備

1. `go build -o cumin ./cmd/cumin` でバイナリを作る。
2. Host で launchd の cumin が動いていれば止める (場面 Check-1 の手順4と同じ理由)。
3. Host の状態ディレクトリ (`~/.local/state/cumin/`) の `state.json` と `quota-allowance.json` を、同じディレクトリの別の名前に移す (例: 末尾に `.before-quota-1` を付ける)。残した使用率があると、cumin が最小の実行を飛ばし (読んだばかりの使用率か、「resume agent starts」)、手順10のログにならないためである。中身は開かない。
4. 前半の設定ファイルを作る。Impl-1 の手順2の形に、次の表を足す。目標を1%にし、前倒しを0にすると、ペースの上限は1%以下になる。5h枠のしきい値は100%にして、5h枠では止まらないようにする。

   ```toml
   [quota.five_hour]
   threshold = 100

   [quota.weekly]
   target = 1
   lead = "0s"
   ```

5. 後半の設定ファイルを作る。Impl-1 の手順2の形に、次の表を足す。weekly枠は、目標を100%、前倒しを7日の直前にして、weekly枠では止まらないようにする。`cumin quota allow` は5h枠にしか効かないので、weekly枠で止まると手順19で着手しない。

   ```toml
   [quota.five_hour]
   threshold = 1

   [quota.weekly]
   target = 100
   lead = "167h"
   ```

6. sandbox に、Impl-1 の手順3と4と同じ要求Issueと実装Issueを作る。実装Issueは、数分で終わる内容にする。
7. sandbox に、`cumin/status/ready` の付いた他の Issue と、同時に進めるIssueの数に数えられるIssueがないことを確かめる (「`cumin run` を sandbox で動かすとき」)。
8. `./cumin status --config <前半の設定ファイル>` で、使用率の欄が `not read yet` であることを確かめる。手順3が効いている。

### 実行: 前半 (weekly枠)

9. 実装Issueに `cumin/status/ready` を付けてから、`./cumin run --config <前半の設定ファイル>` を起動する。先に起動すると、`cumin/status/ready` を付ける前の定期確認で「tell that cumin waits」(待ち状態) の通知が1件出る。
10. 次の定期確認から、ログがこの順に出る。

    | ログの行 | 意味 |
    |---|---|
    | `quota usage read` | 着手の前の最小の実行 |
    | `stop agent starts: the quota limit is reached` (`windows` が `weekly`、`next_try` がweekly枠のリセット時刻) | 着手を止めた。ラベルは替えない |
    | `the notification was sent` | 「stop agent starts」の通知 |

11. 定期確認を3回以上待つ。`quota usage read` は増えない。読んでから5分以内は、残した使用率で判定する。そのあとは、次に試す時刻まで読まない (「resume agent starts」)。`the notification was sent` も増えない。
12. `./cumin status --config <前半の設定ファイル>` を実行する。`agent starts: stopped by the weekly window` が出る。
13. `./cumin quota allow` を実行し、定期確認を2回待つ。着手は起きない (Core-16: 許可はweekly枠のペースの上限を上げない)。
14. SIGTERM で止める。

### 実行: 後半 (5h枠)

15. 状態ディレクトリの `state.json` と `quota-allowance.json` を消す。どちらも前半で作られたものである。残すと、前半の使用率と許可が後半に効く。
16. `./cumin run --config <後半の設定ファイル>` を起動する。実装Issueには `cumin/status/ready` が付いたままである。
17. 次の定期確認から、ログがこの順に出る。

    | ログの行 | 意味 |
    |---|---|
    | `quota usage read` | 着手の前の最小の実行 |
    | `stop agent starts: the quota limit is reached` (`windows` が `5h`、`next_try` が5h枠のリセット時刻) | 着手を止めた |
    | `the notification was sent` | 「stop agent starts」の通知。本文に `cumin quota allow` がある |

18. `./cumin quota allow` を実行する。5h枠のリセット時刻と、weekly枠の上限は変わらないことが表示される。
19. 次の定期確認から、ログがこの順に出る。

    | ログの行 | 意味 |
    |---|---|
    | `quota usage read` | 許可で次に試す時刻の待ちが終わり、読み直した (「resume agent starts」)。手順17の `quota usage read` から5分以内なら、この行は出ない。残した使用率と許可で判定し、次の行に進む |
    | `request the implementation: claimed the issue` | ラベルを `cumin/status/implementing` に替えた |
    | `request the implementation: requested the work`、`agent start`、`agent end`、`the agent run ended` | Implementer の実行 |

20. `the agent run ended` のあとの判定の行 (「wait for the checks」か「stop the implementation」) が出たら、SIGTERM で止める。実行の終わりの結果は、この場面では確かめない。GitHub が `Closes #<番号>` の紐づけを作らないと、「stop the implementation」が「Issue を閉じる開いている Pull Request がない」で止まる (Review-1 の「GitHub が紐づけを作らないとき」)。利用枠とは関係がない。

確かめる枠の使用率が1%未満のときは、1%のしきい値でも止まらない。リセットの直後に起きうる。そのときは、その枠の使用率が1%以上になってからやり直す。確かめない側の枠が100%に達しているとき (使い切ったとき) も、この場面は行えない。

### 確かめること

| # | 確かめること | 見る場所 |
|---|---|---|
| 1 | 前半で、実装Issueのラベルが `cumin/status/ready` のまま変わらない | Issue のイベント |
| 2 | 前半の「stop agent starts」の通知は1件で、weekly枠の停止を知らせている | Discord |
| 3 | 前半で、`quota usage read` は1行だけである。手順11と13の定期確認で増えていない | cumin のログ |
| 4 | 前半で、`cumin quota allow` のあとも着手しない | cumin のログ、Issue のイベント |
| 5 | 後半の「stop agent starts」の通知は1件で、5h枠の停止と `cumin quota allow` を知らせている | Discord |
| 6 | 後半で、`cumin quota allow` のあとの定期確認で着手し (手順17の読み取りから5分を過ぎていれば、使用率を読み直してから)、ラベルが `cumin/status/implementing` に替わった | cumin のログ、Issue のイベント |
| 7 | `cumin status` の表示に、使用率を読んだ時刻、今の上限、止まっている枠が出た | ターミナル |
| 8 | ログと通知に、token、秘密鍵、webhook のアドレス、使用率の数値が出ていない。`cumin status` の表示の数値は、記録に写さない | cumin のログ、Discord |

### 後片付け

- 状態ディレクトリの `state.json` と `quota-allowance.json` を消し、手順3で移したファイルを元の名前に戻す。移す前に `quota-allowance.json` がなかったなら、戻すものはない。
- Pull Request を閉じ、そのブランチを消す。実装Issueと要求Issueを閉じる。
- `work_dir` の一時ディレクトリを消す。
- 手順2で launchd の cumin を止めたなら、戻す。

### 記録

結果は #252 にコメントとして残す。書き方は [Agentの実機の確認](agent-live-check.md) の「記録の決まり」に従う。使用率の数値、リセット時刻、セッションの番号、手元の絶対パス、Client ID、App の名前、webhook のアドレスは書かない。

## 実機の場面 E2E-1

1つの要求Issueを、Maintainer の ready から受け入れの確認まで、launchd で動く cumin に最後まで通させる。分割 (「request the split」、「ask for the plan review」)、着手 (「mark the requirement as in work」、「request the implementation」)、検証 (「wait for the checks」)、レビュー (「request the review」)、`risk/low` の merge (「start the merge」)、`risk/medium` の merge の判断の依頼と承認のあとの merge (「ask for the merge decision」、「start the merge」)、フォローアップノート (「write the follow-up note」)、受け入れの確認 (「request the acceptance check」、「ask for the acceptance」)、通知を、この順に1回で確かめる。

ほかの場面と違い、手順書ではなく Go のテスト `TestLiveE2E` (`cmd/cumin/live_e2e_test.go`) が全体を動かす。テストは Maintainer の代わりをする。要求Issueを書き、risk を確定し、`cumin/status/ready` を付け、`risk/medium` の Pull Request を承認する。それ以外は全て cumin の仕事で、テストはほかの状態ラベルを替えず、merge もコメントもしない。

本物の Claude Code を、Planner で2回、Implementer で2回、Reviewer で2回以上起動する。着手 (「request the split」、「request the implementation」) の前には、読んだばかりの使用率がなければ、使用率の最小の実行も入る。1回の実行は1時間ほどかかる。

### Maintainer の操作に使う認証

- テストは、Host の `gh` のログインで Maintainer の操作を行う。sandbox に write 以上の権限を持つ、人のアカウントでなければならない ([cumin本体の要件](../requirements/cumin-core.md) の「Maintainer、Issue Owner、Operator」)。テストは最初にこれを確かめる。
- このログインは、テストが起動する `gh` のプロセスの中だけで使う。cumin と Agent には渡らない。cumin は launchd が起動した別のプロセスで、App の token だけを使う。
- 実行の間、人もチャットも、cumin の代わりにラベルを替えたり、merge したり、コメントしたり、リンクを張ったり、Issue を閉じたりしない。cumin が先に進めなければ、テストは失敗する。それは不具合として記録し、直してからもう一度通す。

### Host の準備

1. Host の設定ファイル (LaunchAgent に `--config` で渡したもの。テストは plist からそのパスを読む) の `repositories` を sandbox だけにし、`work_dir` を捨ててよいディレクトリにする。`poll_interval` は初期値のままでよい。
2. webhook のアドレスを Keychain に入れ、`notify.discord.enabled` を `true` のままにする (場面 Fail-1 の手順2)。
3. cumin を置き、launchd で起動する ([セットアップの手順](setup-guide.md) の手順4)。

   ```sh
   scripts/install.sh
   ~/.local/bin/cumin setup launchd
   launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/dev.cloveclove.cumin.plist
   ```

   既に動いているなら、`scripts/install.sh --restart` で新しいバイナリに入れ替える。
4. sandbox に、`cumin/status/ready`、`cumin/status/planning`、`cumin/status/implementing`、`cumin/status/checking`、`cumin/status/reviewing` の付いた開いている Issue がないことを確かめる。あれば、テストは何も作らずに止まる。

テストは launchd の job を入れることも、止めることもしない。動いていることを確かめるだけである。

### 実行

```sh
CUMIN_LIVE=1 CUMIN_LIVE_REPO=<owner>/<repo> go test -count=1 -timeout 4h -run TestLiveE2E -v ./cmd/cumin/
```

テストがすること:

1. 要求Issueを作り、`cumin/type/requirement` と `cumin/status/ready` を付ける。内容は、`live/e2e-<日時>/` の下の小さなファイル2つである。どちらの Pull Request にも `Follow-up` に1行を残させるので、フォローアップノートが必ず2つ付く。
2. 分割を待ち、sub-issue が2つで、risk が1つずつ付いていることを確かめる。
3. Maintainer として risk を確定する。Planner が `risk/low` と `risk/medium` を1つずつ付けていれば、そのままにする。そうでなければ、番号の小さいほうを `risk/medium`、大きいほうを `risk/low` にする。そのあと、2つに `cumin/status/ready` を付ける。
4. `risk/medium` の実装Issueが `cumin/status/awaiting-merge-decision` に移ったら、Pull Request がまだ merge されていないことを確かめてから、`gh pr review --approve` で承認する。
5. 2つの実装Issueが閉じ、受け入れの確認のコメントが付き、要求Issueが `cumin/status/awaiting-acceptance` に移るのを待つ。
6. 最後に、段階ごとの証拠の表を Markdown で出力する。

途中でどれかの Issue が `cumin/status/awaiting-decision` に移ったら、テストはそこで失敗する。

### テストが確かめること

| # | 確かめること | 見る事実 |
|---|---|---|
| 1 | 要求Issueの状態が `ready`、`planning`、`awaiting-plan-review`、`implementing`、`accepting`、`awaiting-acceptance` の順に移った。`ready` を付けたのは Maintainer で、ほかは cumin-core の App である | Issue のイベント |
| 2 | sub-issue が2つで、作成者が Planner の App である。risk が1つずつ付いている。`## Plan for approval` のコメントが1つある | sub-issue の一覧、コメント |
| 3 | 実装Issueの状態が `ready`、`implementing`、`checking`、`reviewing` の順に始まる。`risk/medium` は最後に `awaiting-merge-decision` に移る | Issue のイベント |
| 4 | 実装Issueごとに Pull Request がちょうど1つある。作成者は Implementer の App、ブランチは `cumin/<Issue番号>-...`、本文に `Closes #<Issue番号>` がある | 閉じるリンク、Pull Request |
| 5 | Reviewer の App の最後のレビューが、先頭のコミットへの `APPROVED` である | レビュー |
| 6 | merge したのが cumin-core の App で、merge のコミットの親が1つである。merge の方法は squash である。実装Issueが `completed` で閉じた | Pull Request、コミット、Issue、ログの `merge_method` |
| 7 | `risk/medium` の Pull Request は、Maintainer が承認するまで merge されず、merge は先頭のコミットへの Maintainer の承認のあとである | Pull Request、レビューの時刻 |
| 8 | Pull Request ごとに、cumin-core の App のフォローアップノートが目印付きで1つあり、受け入れの確認より前に書かれている。ノートにあるのは `Follow-up` の1行だけで、CLI の署名は入っていない | 要求Issueのコメント |
| 8a | 分割の全体像、レビューの本文、受け入れの確認が、テンプレートの `###` の見出しを持っている | コメント、レビュー |
| 9 | `## Acceptance check` で始まる Planner の App のコメントが1つあり、最後の実装Issueが閉じたあとに書かれている。要求Issueが `awaiting-acceptance` に移ったのは、そのあとである | 要求Issueのコメント、イベント |
| 10 | cumin のログに、Issue ごとの動作の行がこの順にある | `~/.local/state/cumin/cumin.log` の、テストの開始よりあとの行 |
| 11 | 通知の行 (`the notification was sent`) が、分割結果の確認 (「ask for the plan review」)、merge の判断 (「ask for the merge decision」)、受け入れ (「ask for the acceptance」) で1行ずつある | 同じログ |
| 12 | ログに token、秘密鍵、webhook のアドレス、使用率の数値がない | 同じログ |
| 13 | launchd の job のプロセスが、最初から最後まで同じである | `launchctl print` |

### 手で見ること

人の判断が要るものだけを、実行のあとに目で見る。

- 分割の質。sub-issue の本文、図、完了条件が、実装できる内容になっている。
- レビューの質と、受け入れの確認の根拠。
- Discord に通知が届き、リンクが対象の Issue か Pull Request を開く。
- 受け入れ。Maintainer が要求Issueを閉じる。

### 後片付け

- 要求Issueを閉じる。2つのファイルは main に残る。名前に日時があるので、次の実行の邪魔にならない。
- 途中で失敗したときは、開いたままの sub-issue、要求Issue、Pull Request を閉じ、ブランチを消す。作業中のラベルのまま残すと、次の実行の最初の確認で止まる。
- launchd の cumin を残さないなら、`cumin setup launchd --remove` で外す。

### 記録

結果は #292 にコメントとして残す。テストが出力した表をそのまま貼る。書き方は [Agentの実機の確認](agent-live-check.md) の「記録の決まり」に従う。使用率の数値、token、webhook のアドレス、セッションの番号、手元の絶対パス、Client ID は書かない。
