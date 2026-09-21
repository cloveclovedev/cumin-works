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
- GitHub Appのtokenの発行と、1回の依頼の手順 (使用率の確認、tokenの発行、起動)。要求Issue #8 の #69 が、この文書に話題を足す。

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

### Claude Codeの起動

![1回の実行](agent-run.svg)

図の元ファイル: [agent-run.puml](agent-run.puml)

- 依頼のたびに `claude -p` を、worktreeを作業ディレクトリにして起動する。標準入力はnullデバイスにする。標準入力が開いたまま何も来ないと、CLIは3秒待ってから進む (実測。Claude Code 2.1.267)。プロセスは依頼が終わったら終了する。
- オプション (公式: Run Claude Code programmatically、CLI reference):
  - `--append-system-prompt <roleの指示>`: roleの指示は、システムプロンプトの末尾に足す。Claude Code既定のシステムプロンプト (ツールの使い方、リポジトリの `CLAUDE.md`) はそのまま使う。
  - `-p <依頼文>`: 依頼文は、プロンプトの引数で渡す。
  - `--setting-sources project`: ユーザアカウントの設定と `CLAUDE.md` を読ませない (実測 6e、27)。
  - `--permission-mode bypassPermissions`: 全てのツールを許可する。headlessの実行では、許可を尋ねられても答える人がいない。`--dangerously-skip-permissions` と同じ意味である (CLI reference)。
  - `--json-schema <結果のスキーマ>`: 結果を [共通の形式](../requirements/agents/common.md) に従わせる。スキーマの文字列は、コードで要件文書と同じに保つ。
  - `--output-format stream-json --verbose`: イベントを1行ずつ読む。`--json-schema` と併用できる (実測 26)。
  - `--model <モデル>`: 設定 `roles.<role>.model` が空でないときだけ付ける。
  - `--resume <セッションの番号>`: 続きの依頼のときだけ付ける (実測 31)。roleの指示と依頼文は、続きの依頼でも渡す。
- 採らなかった案: `--bare`。サブスクリプションのログインを使えない (実測 6a)。
- 採らなかった案: `--allowedTools` でツールを列挙する。roleごとの制限は、GitHub Appの権限とrulesetで行う (cumin本体の要件)。
- 採らなかった案: roleの指示を依頼文に含める。続きの依頼で指示を二重に渡すことになり、システムプロンプトとしての扱いも受けない。
- 実行ファイルは設定 `roles.<role>.cli_path` で決める。受け入れテストは、決まった出力を返すシェルスクリプトを指す。

### 出力の読み取り

- 標準出力の1行を1つのJSONとして読み、`type` で見分ける。読むのは3種類だけで、ほかは飛ばす。
  - `system` の `init`: セッションの番号 (`session_id`)。
  - `rate_limit_event`: `rate_limit_info.unifiedWindows` の `five_hour` と `seven_day` の、`utilization` (0から1) と `resetsAt` (Unix秒) (実測 1)。1回の実行に複数回出るので、最後のものを使用率とする。
  - `result`: `session_id`、`subtype`、`is_error`、`structured_output` (実測 26)。
- 正常終了は、終了コードが0で、`result` があり、`is_error` が偽で、`structured_output` がスキーマに合い、`blocked` なら理由が空でないときである。cuminのほかの部分には、セッションの番号、結果、使用率を返す。
- `rate_limit_event` がなくても異常終了にしない。使用率は「読めなかった」として返す。着手の前の使用率の確認は、別の話題 (cumin本体の設計メモ) が受け持つ。
- 異常終了は、種類を付けて返す。プロセスの失敗 (起動できない、終了コードが0でない)、`result` がない、`is_error` が真、結果がスキーマに合わない、実行時間の上限。やり直すかどうかは、cuminのほかの部分が種類で決める。
- 使用率の数値は、debugのログにだけ出す。infoのログには、セッションの番号、結果、異常終了の種類を出す。標準エラー出力は、debugのログに出す。
- `rate_limit_event` の形は公式ドキュメントにない (実測 2)。形が変わったら、使用率は「読めなかった」になる。2026-09-20 に、最小の実機実行で、3つのイベントの項目名と `result` の `subtype: "success"` を確かめた。

### Agentの環境

- CLIのプロセスの環境変数は、cuminの環境を引き継がずに、決まった一覧から組み立てる。Hostから渡すのは `PATH`、`HOME`、`TMPDIR`、`LANG`、`LC_ALL`、`LC_CTYPE`、`SHELL`、`USER`、`LOGNAME` だけである。Hostのユーザの `SSH_AUTH_SOCK`、`GH_TOKEN`、`GITHUB_TOKEN`、`ANTHROPIC_API_KEY` は、この一覧にないので届かない。`HOME` を残すのは、Claude Codeがサブスクリプションのログインとセッションの記録を `~/.claude` の下で探すためである。
- 自動メモリは、環境変数 `CLAUDE_CODE_DISABLE_AUTO_MEMORY=1` で切る。`--setting-sources project` では止まらない (実測 28、公式: Environment variables)。
- gitには、Hostのユーザとシステムの設定ファイルを読ませない。`GIT_CONFIG_GLOBAL=/dev/null` と `GIT_CONFIG_NOSYSTEM=1` で、credential helperとOwnerの作者の設定が見えなくなる (公式: git の環境変数)。`GIT_TERMINAL_PROMPT=0` で、パスワードの入力待ちにしない。`GIT_SSH_COMMAND=false` で、SSHの接続を必ず失敗させる。worktreeのremoteはHTTPSなので、Hostの鍵を使う道がない。
- roleのtokenは、`GIT_CONFIG_COUNT=1`、`GIT_CONFIG_KEY_0=http.https://github.com/.extraheader`、`GIT_CONFIG_VALUE_0=Authorization: Basic <x-access-token:token のbase64>` で渡す (公式: git-config の環境変数)。ファイルにも引数にも書かない。
- コミットの作者は、roleのAppのbotのユーザにする。`GIT_AUTHOR_NAME` と `GIT_COMMITTER_NAME` は `<slug>[bot]`、`GIT_AUTHOR_EMAIL` と `GIT_COMMITTER_EMAIL` は `<botのuser id>+<slug>[bot]@users.noreply.github.com` である。この形なら、GitHubがコミットをbotのユーザに結び付ける (実測 49)。作者を渡さないと、gitはHostのユーザ名とホスト名から作者を推測してコミットする (2026-09-21に実機で確かめた。Apple Git 2.50.1)。
- ghには、`GH_TOKEN` でroleのtokenを渡す。ghは、保存されたログインより `GH_TOKEN` を優先する。`GH_CONFIG_DIR` は、依頼のたびに作る空のディレクトリにして、Hostのユーザの設定を読ませない。ghは空のディレクトリで既定の設定 (`git_protocol` は `https`) を使う (2026-09-21に実機で確かめた。gh 2.101.0)。`GH_PROMPT_DISABLED=1` と `GH_NO_UPDATE_NOTIFIER=1` で、入力待ちと更新の通知を止める (公式: gh help environment)。ディレクトリは、依頼が終わったら消す。
- この環境は、うっかり使うことを防ぐだけである。Agentのプロセスは `HOME` を持つので、意図して `~/.ssh` や `~/.claude` のファイルを読むAgentは止められない。roleごとの制限は、GitHub Appの権限とrulesetで行う。
- 採らなかった案: `CLAUDE_CODE_SUBPROCESS_ENV_SCRUB`。Bashツールの環境から認証情報を取り除く機能だが、Agentはそこでroleのtokenを使う。
- 採らなかった案: cuminの環境を引き継いで、危ない変数だけを外す。外し忘れた変数がそのまま届く。
- 採らなかった案: Agent用の別のOSユーザ。要求のbacklogにある。

### 実行時間の上限

- 依頼ごとの上限は、設定 `roles.<role>.time_limit` の値である。上限を過ぎたら打ち切り、異常終了 (種類は「実行時間の上限」) として返す。cuminのほかの部分が依頼を取り消したときも、同じ種類にする。
- 打ち切りは2段階にする。まずCLIのプロセスグループにSIGTERMを送る。CLIは、SIGTERMを受けると終了コード143で終わり、実行中のBashコマンドのプロセスツリーを止める (公式: Run Claude Code programmatically の "Stop a run with SIGTERM"、実測 32)。猶予 (10秒) の間に終わらなければ、プロセスグループにSIGKILLを送る。
- CLIは自分のプロセスグループで起動する (`Setpgid`)。Agentが起動したコマンドが同じグループに入るので、シグナルがそこまで届く。
- Goの `os/exec` の `Cmd.Cancel` と `Cmd.WaitDelay` を使う。`Cancel` がSIGTERMを送り、`WaitDelay` (猶予と同じ値) が過ぎたら `os/exec` がCLIを止めてパイプを閉じる。そのあとで、cuminがグループにSIGKILLを送り、残ったものを消す。
- 標準出力は、行が届くたびに読む (`Cmd.Stdout` に書き込み先を渡す)。プロセスが終わった時点でパイプに残っていた行も、`os/exec` が読み切ってから `Wait` が返る。
- 実機で確かめたこと (2026-09-20、Claude Code 2.1.267): Bashで `sleep 600` を実行中のCLIに、上限でSIGTERMを送ると、CLIは猶予を待たずに終わり、プロセスグループには何も残らなかった。手順は [Agentの実機の確認](../development/agent-live-check.md) にある。
- 採らなかった案: `--max-turns`。手元のCLIのヘルプにないうえ、ターンの数は時間の上限にならない (実測 32)。
- 採らなかった案: SIGKILLだけ。CLIがセッションを記録し、Bashのツリーを止める機会がなくなる。

## まだ決めていないこと

| 決める、または確かめること | どこで |
|---|---|
| Reviewerの作業場所。Pull Requestのブランチを、同じIssueのImplementerのworktreeと同時に開けるか | Reviewerへの依頼を作る要求Issue |

## 後回しにしたこと

- private リポジトリのcloneとfetchに使う認証情報。v0.1は、認証なしのHTTPSでcloneする。きっかけ: 対象にprivateリポジトリが加わったとき。
