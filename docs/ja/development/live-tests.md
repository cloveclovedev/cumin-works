# 実機の確認 (live test)

本物の GitHub App と、使い捨てのリポジトリ (sandbox) を使って、公式ドキュメントでは分からない GitHub の振る舞いを確かめるテスト。ふだんの `go test ./...` では動かない。

## いつ実行するか

- Owner が同意したときだけ実行する。
- ある Organization に cumin-works を導入した直後に、セットアップが正しいことを確かめるために実行する。
- GitHub の振る舞いが変わった疑いがあるときに、実行し直す。

ほとんどのテストは Claude Code を起動しないので、利用枠を使わない。起動するものには、下の表と「実行のしかた」で断りを書く。

## 前提

- [セットアップの手順](setup-guide.md) の手順1〜3が済んでいる。
  - 4つの GitHub App が登録され、sandbox のリポジトリにインストールされている。
  - Host の設定ファイルに Client ID があり、Keychain に秘密鍵がある。
  - sandbox に `scripts/setup-repo.sh <owner>/<repo> --core-app <slug>` を実行してある。
- sandbox は、公開のリポジトリである。App の token には Checks の権限がなく、check の結果を読めるのは公開のリポジトリだけだからである。テストは最初に、認証なしでリポジトリを読めることを確かめ、読めなければ止まる。
- sandbox の保護されたパスの一覧 (`.cumin/config.toml` の `protected_paths`) に `CLAUDE.md` があり、`live/` を守っていない。テストは `live/CLAUDE.md` (保護されている) と `live/<日時>.md` (保護されていない) を使う。ファイルがなければ、初期値の一覧が使われるので、そのままでよい。合っていなければ、テストは Pull Request を作る前に止まる。
- `TestLiveGitHubFacts` には、sandbox に次の2つが要る。リポジトリの管理者が用意する。
  - `internal/platform/github/testdata/cumin-live-fixture.yml` を `.github/workflows/cumin-live-fixture.yml` として、既定のブランチに置く。main は保護されているので、Pull Request で入れる。
  - `scripts/setup-repo.sh <owner>/<repo> --core-app <slug> --required-check live-skipped-for-bots` を実行して、fixture の job を必須のcheckにする。
  - 足りなければ、テストは最初に止まって、足りないものを表示する。
- sandbox は、壊れてもよいリポジトリである。テストは Issue、Pull Request、ブランチ、ラベルを作り、main に小さなファイルを1つ merge する。

## 実行のしかた

```sh
CUMIN_LIVE=1 CUMIN_LIVE_REPO=<owner>/<repo> go test -count=1 -run TestLive -v ./internal/platform/github/
# Agent の環境の確認 (internal/agent。Claude Code は起動しない)
CUMIN_LIVE=1 CUMIN_LIVE_REPO=<owner>/<repo> go test -race -count=1 -run TestLive_AgentEnvironment -v ./internal/agent/
# 本物の Claude Code に commit、push、Pull Request をさせる確認 (利用枠を使う。Owner が同意したときだけ)
CUMIN_LIVE=1 CUMIN_LIVE_REPO=<owner>/<repo> go test -race -count=1 -run TestLive_AgentRunOnSandbox -v ./internal/agent/
```

| 環境変数 | 内容 |
|---|---|
| `CUMIN_LIVE` | `1` のときだけ実行する。それ以外では skip する |
| `CUMIN_LIVE_REPO` | sandbox のリポジトリ。`<owner>/<repo>` の形 |
| `CUMIN_CONFIG` | Host の設定ファイル。省くと `~/.config/cumin/config.toml` |
| `CUMIN_LIVE_MENTION` | 任意。GitHub の login。指定すると、`TestLiveGitHubFacts` が、その人を@メンションするコメントを1つ投稿する。通知が届いたかは、その人が目で確かめる |

テストの最後に、結果の表 (番号、確かめたこと、期待、実際の結果) が Markdown で出力される。

## token の扱い

- テストは、Host の設定の Client ID と Keychain の秘密鍵から、App ごとに installation access token を発行する。token は、sandbox のリポジトリ1つと、その App の権限に絞られる。
- token は、テストのプロセスの中だけで使う。コマンドの引数、リモートのアドレス、ファイル、テストの出力には現れない。
- git には、環境変数 (`GIT_CONFIG_*`) で認証のヘッダを渡す。ユーザの git の設定は読まない。Owner の認証情報は使わない。

## 後片付け

fixture の workflow は、sandbox の全ての Pull Request で動く。`live-fail-on-marker` は `live/fail-marker` を変える Pull Request で失敗し、`live-skipped-for-bots` は bot (GitHub App) の Pull Request で飛ばされ、`live-commit-status` は commit status を1つ作る。

テストは、Pull Request、ブランチ、Issue を作った直後に、後片付けを登録する。テストが途中で失敗しても、作ったものは閉じられ、消される。main に merge した小さなファイル (`live/<日時>.md`) は残る。

## 確かめる内容

| テスト | 内容 |
|---|---|
| `TestLiveSetupChecks` | [GitHub Appの登録手順](github-app-setup.md) の「確認すること」。main への push と merge の拒否、cumin-core による merge、Issue と sub-issue と blocked by、approve、表示名、保護されたパスのcheck、作成者の種類 |
| `TestLive_AgentEnvironment` (`internal/agent`) | cumin が Agent のために組み立てる環境 ([Agentの実行の設計](../designs/agent-run.md) の「Agentの環境」) で、本物の git と gh を動かす。Implementer App の token と bot の身元を `Service` と同じ手順で用意し、sandbox の worktree で `git config --show-origin` に Host のファイルが出ないこと、commit と push と `gh pr create` が通ること、Pull Request の作成者と commit の作者が Implementer App の bot であることを確かめる。Claude Code は起動せず、利用枠を使わない。Pull Request は閉じ、ブランチと worktree は消す |
| `TestLive_AgentRunOnSandbox` (`internal/agent`) | 本物の Claude Code を `Service.Start` で起動し (使用率の最小実行と Implementer の実行の 2 回、利用枠を使う)、sandbox の worktree でファイルを 1 つ commit して push し、`gh pr create` で Pull Request を開かせる。Pull Request の作成者と commit の作者が Implementer App の bot であることを確かめる。起動の記録の確認 (`init` イベント) も本物の実行で通る。Pull Request は閉じ、ブランチと worktree は消す |
| `TestLiveGitHubFacts` | cumin の実装が前提にする GitHub の事実。check run と commit status の読み取り、token の絞り込み、飛ばされた必須のcheckと merge、失敗したcheckについて読める範囲、レビューの `state` と `commit_id`、`Closes #N` で sub-issue が閉じること、GraphQL の項目、`rules/branches`、@メンション、Pull Request へのラベル |

## `cumin run` を sandbox で動かすとき

定期確認や着手を sandbox で確かめるときは、`go build -o cumin ./cmd/cumin` で組み込んだバイナリを動かす。`go run` で動かすと、親のプロセスに送った SIGTERM が `cumin` の子プロセスに届かず、止め方の確認にならない (2026-09-21 に確かめた)。launchd は組み込んだバイナリを起動するので、Host の運用には関係しない。

`cumin run` は、`cumin/status/ready` の付いた sub-issue を見つけると、本物の Implementer を起動する。利用枠を使う (使用率の最小の実行と Implementer の実行で2回)。Owner が同意したときだけ動かす。動かす前に確かめること。

- Host の設定ファイルに、sandbox のリポジトリと、4つの App (`cumin-core` と3つの role) の Client ID がある。秘密鍵が Keychain にある。
- `work_dir` が、捨ててよいディレクトリを指している。cumin はその下に clone と worktree を作る。
- sandbox に、`cumin/status/ready` の付いた sub-issue が、確かめたいものだけある。ほかに ready の sub-issue があると、そちらにも着手する。

止めるときは SIGTERM を送る。動いている Implementer の実行が終わるまで待つので、すぐには終わらない。実行を待たずに終わらせたいときは、もう一度 SIGTERM を送らずに、実行の時間の上限 (`roles.implementer.time_limit`) を短くした設定で動かし直す。

## 実機の場面 Impl-1

`cumin/status/ready` の付いた実装Issueから、Pull Request が開いて `cumin/status/awaiting-checks` に移るまでを、1回通して確かめる。本物の Claude Code を2回起動する (使用率の最小の実行と、Implementer の実行) ので、利用枠を使う。Owner が同意したときだけ行う。

### 準備

1. `go build -o cumin ./cmd/cumin` でバイナリを作る。
2. この場面だけの設定ファイルを1つ作る。Host の設定ファイルとは別にして、対象を sandbox だけにする。`work_dir` は捨ててよい一時ディレクトリにする。`github_apps` の表は Host の設定ファイルから写す。

   ```toml
   repositories = ["<owner>/<repo>"]
   work_dir = "<捨ててよい一時ディレクトリ>"
   poll_interval = "20s"

   [roles.implementer]
   time_limit = "20m"

   [github_apps.<owner>]
   cumin-core = "<Client ID>"
   planner = "<Client ID>"
   implementer = "<Client ID>"
   reviewer = "<Client ID>"
   ```

3. sandbox に要求Issueを1つ作り、`cumin/type/requirement` と `cumin/status/implementing` を付ける。`cumin/status/ready` は付けない。付けると R1 が成り立ち、Planner の分割まで走ってしまう。
4. その sub-issue として実装Issueを1つ作り、`risk/low` を付ける。数分で終わる内容にする。保護されたパスを触らせない (例: `live/` の下にファイルを1つ作って1行書く)。題はブランチの名前になるので、短い英語にする。
5. sandbox に `cumin/status/ready` の付いた他の sub-issue がないことを確かめる。あると、そちらにも着手する。

### 実行

6. `./cumin run --config <設定ファイル>` を起動する。起動時のログは `skills written`、`cumin run starts`、`poll` の順に出る。足りないラベルがあれば、`cumin run starts` と `poll` の間に `created the label` が出る。Keychain に webhook のアドレスがなければ、`cumin run starts` の前に警告が1行出て、`notifications` は `none` になる ([セットアップの手順](setup-guide.md) の「通知のアドレスを Keychain に入れる」)。
7. 実装Issueに `cumin/status/ready` を付ける。
8. 次の定期確認から、ログがこの順に出る。

   | ログの行 | 意味 |
   |---|---|
   | `I1: claimed the issue` | ラベルを `cumin/status/implementing` に替えた |
   | `clone created`、`worktree created` | 作業場所を用意した |
   | `I1: requested the work` | ブランチの名前を決めて、Implementer を起動した |
   | `quota usage read` | 使用率の最小の実行が終わった |
   | `agent token created`、`agent identity read` | roleのtokenとbotの身元 |
   | `agent start`、`agent end` | Claude Code の実行の始まりと終わり |
   | `the agent run ended` | 結果 (`done` か `blocked`) とセッションの番号 |
   | `I2: verified the pull request` | 検証が通り、ラベルを `cumin/status/awaiting-checks` に替えた |

9. `I2: verified the pull request` が出たら、SIGTERM で止める。

### 確かめること

| # | 確かめること | 見る場所 |
|---|---|---|
| 1 | 実装Issueのラベルが `cumin/status/ready` から `cumin/status/implementing` を経て `cumin/status/awaiting-checks` に移った。状態ラベルは常に1つだけ | Issue のイベント |
| 2 | Pull Request がちょうど1つ開いている。ブランチは `cumin/<Issue番号>-<短い説明>` で、`I1: requested the work` の `branch` と同じ | Pull Request |
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

Implementer が `blocked` を返したときに、cumin が理由をIssueに書き、ラベルを `cumin/status/awaiting-owner-decision` に替え、Discord に通知を1件送るところを、1回通して確かめる。本物の Claude Code を2回起動する (使用率の最小の実行と、Implementer の実行) ので、利用枠を使う。Owner が同意したときだけ行う。

受け入れテストは偽の webhook を相手にするので、本物のメッセージが本物のチャンネルに届くことと、そのリンクが開くことは、この場面でだけ分かる。

### 準備

1. `go build -o cumin ./cmd/cumin` でバイナリを作る。
2. webhook のアドレスを Keychain に入れる。まだなら `cumin setup notify --discord-webhook` を実行する ([セットアップの手順](setup-guide.md) の「通知のアドレスを Keychain に入れる」)。
3. 場面 Impl-1 と同じ形の設定ファイルを1つ作る。対象は sandbox だけ、`work_dir` は捨ててよい一時ディレクトリにする。`notify.discord.enabled` は初期値の `true` のままにする。
4. sandbox に要求Issueを1つ作り、`cumin/type/requirement` と `cumin/status/implementing` を付ける。`cumin/status/ready` は付けない。
5. その sub-issue として実装Issueを1つ作り、`risk/low` を付ける。Implementer が必ず `blocked` を返す内容にする。決まっていないことが1つあり、推測では進めないと分かる題と本文にする。Agent は、決まっていないことに当たったら `blocked` を返す ([Agentに共通の要件](../requirements/agents/common.md))。
6. sandbox に `cumin/status/ready` の付いた他の sub-issue がないことを確かめる。

### 実行

7. `./cumin run --config <設定ファイル>` を起動する。起動のログの `notifications` が `discord` であることを確かめる。`none` なら、手順2ができていない。
8. 実装Issueに `cumin/status/ready` を付ける。
9. 次の定期確認から、ログがこの順に出る。

   | ログの行 | 意味 |
   |---|---|
   | `I1: claimed the issue` | ラベルを `cumin/status/implementing` に替えた |
   | `I1: requested the work` | ブランチの名前を決めて、Implementer を起動した |
   | `the agent run ended` | 結果が `blocked` で返った |
   | `the agent returned blocked` | 理由の1行目 |
   | `I2: wrote the reason on the issue` | `blocked_reason` をコメントとして投稿した |
   | `I2: the issue waits for the Owner` | ラベルを `cumin/status/awaiting-owner-decision` に替えた |
   | `the Owner was notified` | Discord に送った |

10. `the Owner was notified` が出たら、SIGTERM で止める。

### 確かめること

| # | 確かめること | 見る場所 |
|---|---|---|
| 1 | 実装Issueのラベルが `cumin/status/ready` から `cumin/status/implementing` を経て `cumin/status/awaiting-owner-decision` に移った。状態ラベルは常に1つだけ | Issue のイベント |
| 2 | 実装Issueにコメントが1つ付き、`## Decision needed:` で始まる決まった形式である ([decision-request.md](../../../templates/decision-request.md)) | Issue のコメント |
| 3 | コメントの作成者が cumin本体の App の bot (`cumin-core[bot]`) である。文章はAgentが書き、投稿するのは cumin だからである | Issue のコメント |
| 4 | Pull Request が作られていない。やり直しも起きていない (Claude Code の起動は2回だけ) | Pull Request の一覧と、cumin のログ |
| 5 | Discord にメッセージが1件だけ届いた。1行目に `I2` と理由が入っている | Discord のチャンネル |
| 6 | そのメッセージのリンクが、2のコメントを開く | Discord のメッセージ |
| 7 | ログに token、秘密鍵、webhook のアドレス、使用率の数値が出ていない | cumin のログ |

### 後片付け

- 実装Issueと要求Issueを閉じる。
- `work_dir` の一時ディレクトリを消す。
- Discord のメッセージは残してよい。消すなら、Owner が自分で消す。

### 記録

結果は #139 にコメントとして残す。書き方は [Agentの実機の確認](agent-live-check.md) の「記録の決まり」に従う。使用率の数値、セッションの番号、手元の絶対パス、Client ID、App の名前、webhook のアドレスは書かない。
