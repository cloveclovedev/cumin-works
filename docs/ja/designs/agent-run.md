# Agentの実行の設計

- 状態: Approved
- 要件: [cumin本体の要件](../requirements/cumin-core.md) の「Agentの起動」、[Agentに共通の要件](../requirements/agents/common.md) の「指示の渡し方」と「プロセスとセッション」
- 事実の出どころ: [調査・実測で確定した制約](../requirements/evidence/measured-constraints.md) の行の番号 (「実測 N」と書く) か、公式ドキュメントのページの名前 (git、Claude Code) で示す。

Agentを1回起動して、結果を受け取るまでの、Hostの側の設計を書く。作業場所の用意と片付け、CLIの起動、出力の読み取り、実行時間の上限がこれに当たる。cuminのほかの部分は、この設計を通してだけAgentに触れる。

## 範囲

扱うこと:

- 1回の依頼を、起動から結果までの1つの手順として扱うために決めること。Agentを動かすCLIに固有のことは、`internal/agent` の接続部分に閉じ込め、ほかの部分には、起動、セッションの番号、結果、使用率、異常終了だけを見せる。

扱わないこと:

- どの条件でAgentを起動するか。[Issueのラベルと状態遷移](../requirements/workflow/issue-states.md) にある。
- 着手の直前に使用率を読むこと。[cumin本体の設計メモ](cumin-core.md) の「起動前の使用率の確認」にある。
- roleごとの指示の内容。各roleの要件文書と `roles/` にある。
- GitHub Appのtokenと、Agentの環境の隔離。要求Issue #8 が、この文書に話題を足す。

## 設計

### 作業場所

![作業場所の配置](agent-run-layout.svg)

図の元ファイル: [agent-run-layout.puml](agent-run-layout.puml)

- 対象のリポジトリごとに、`<work_dir>/<owner>/<repo>/clone` にcloneを1つ置く。`git clone --no-checkout` で作り、作業ファイルを置かない。worktreeの親としてだけ使う (公式: git-clone の `--no-checkout`)。一度作ったら使い回し、着手のたびに `git fetch --prune origin` で更新する。fetchは `origin/HEAD` を動かさないので、続けて `git remote set-head origin --auto` で、リモートの既定のブランチを問い合わせる (公式: git-remote)。
- worktreeは `<work_dir>/<owner>/<repo>/<Issue番号>-<role>` に、Issueとroleごとに1つ作る。並行して進むIssueの作業が混ざらない。
- 書くrole (Implementer) のworktreeは、cuminが決めた名前のブランチにする。`origin/<ブランチ>` があればそこから始める (続きの依頼)。なければ `origin/HEAD` から新しく作る。読むだけのrole (Chief Engineer) のworktreeは、`origin/HEAD` のdetachedにする。
- worktreeが既にあれば、fetchもせずに、そのまま返す。異常終了のやり直しは、同じ作業場所で続く。gitがworktreeと認めるディレクトリだけを再利用し、途中で止まった用意が残した空のディレクトリは作り直す。ディレクトリだけが消えていたら、`git worktree prune` で登録を消してから作り直す。
- 用意と片付けは、プロセスの中で直列にする。同じリポジトリの2つのIssueが同時に着手しても、cloneは1回しか作られない。
- Issueが閉じたら、`git worktree remove --force` でworktreeを消し、ローカルのブランチも消す。呼ぶのは定期確認である。
- gitは `os/exec` で呼ぶ。`GIT_TERMINAL_PROMPT=0` を付け、認証の入力待ちで止まらないようにする (公式: git の環境変数)。gitの出力は、エラーの文章にだけ入れ、infoのログには出さない。
- 採らなかった案: Issueごとにcloneする。毎回リポジトリの全体を取り直すので、遅く、ディスクを使う。
- 採らなかった案: cloneにmainをチェックアウトしておく。Agentが誤ってそこで作業しうる。同じブランチを2か所でチェックアウトできないので、worktreeの邪魔にもなる。
- 採らなかった案: bare clone。remote-tracking branchとその設定が作られず (公式: git-clone の `--bare`)、fetchの設定を自分で足すことになる。

## まだ決めていないこと

| 決める、または確かめること | どこで |
|---|---|
| Claude Codeの起動の仕方と、出力の読み取り | #40 |
| 実行時間の上限と、打ち切りの仕方 | #41 |
| Reviewerの作業場所。Pull Requestのブランチを、同じIssueのImplementerのworktreeと同時に開けるか | Reviewerへの依頼を作る要求Issue |

## 後回しにしたこと

- private リポジトリのcloneとfetchに使う認証情報。v0.1は、認証なしのHTTPSでcloneする。きっかけ: 対象にprivateリポジトリが加わったとき。
