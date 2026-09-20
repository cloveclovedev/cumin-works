# 調査・実測で確定した制約 (v2)

v2の要求整理の中で調べた事実だけを集める。設計上の決定は含まない。v1の実測は、v1のリポジトリの `docs/ja/measured-constraints.md` にある。

確度の凡例: 実測 = このホストで実際に動かして観測した、公式文書 = 公式ドキュメントで確認した、未確認 = 公式文書に記載が見つからず、まだ試していない。

## 1. Claude Codeの利用枠 (2026-09-19、Claude Code 2.1.267)

| # | 制約 | 根拠 | 確度 |
|---|---|---|---|
| 1 | headless実行 (`claude -p ... --output-format stream-json --verbose`) の出力に `rate_limit_event` が含まれ、5h枠とweekly枠の使用率とリセット時刻を機械可読で読める | `rate_limit_info.unifiedWindows.five_hour` と `seven_day` のそれぞれに、`utilization` (0から1の使用率) と `resetsAt` (Unix秒) がある。`status: "allowed"` も返る。同時刻の `/usage` の表示と値が一致した | 実測 |
| 2 | `rate_limit_event` の仕様は、確認した公式文書 (headless、Agent SDK TypeScriptリファレンス) には見つからなかった。バージョンアップで変わりうる | 同左 | 未確認 |
| 3 | `claude -p "/usage"` はheadlessで動き、使用率とリセット時刻を人間向けテキストで返す。`--output-format json` でもテキストが `result` に入るだけ | 出力例: `Current session: <N>% used · resets <日時> (<タイムゾーン>)` | 実測 + 公式文書 |
| 4 | statuslineスクリプトへのJSONには `rate_limits.five_hour.used_percentage` と `resets_at` (Unix秒) などがある。Pro/Max加入者のみ、セッション内の最初のAPI応答後にだけ現れる | https://code.claude.com/docs/en/statusline.md | 公式文書 |
| 5 | Anthropic APIのRate Limits API、Usage & Cost APIはAPI組織向けで、サブスクリプションの5h枠・weekly枠は返さない | https://platform.claude.com/docs/en/manage-claude/rate-limits-api.md | 公式文書 |
| 6 | 枠の上限に当たったときのheadless実行の振る舞い (エラーの形、`status` の値) | 試していない | 未確認 |
| 6a | `claude -p` の `--bare` は、hooks、skills、plugins、MCPサーバ、自動メモリ、CLAUDE.md の自動読み込みを全て省く。ただしサブスクリプションのログインを使えず、`ANTHROPIC_API_KEY` などが要る。利用枠でAgentを動かすcuminでは使えない | https://code.claude.com/docs/en/headless.md: "bare mode doesn't use your subscription login"、"In bare mode, Claude Code never reads OAuth credentials or the system keychain." | 公式文書 |
| 6b | `--bare` は将来 `-p` の既定になる予定と書かれている。そうなったとき、サブスクリプションのログインでheadless実行を続ける方法を確かめる必要がある | 同上: "`--bare` is the recommended mode for scripted and SDK calls, and will become the default for `-p` in a future release." | 公式文書 |
| 6c | `--bare` なしの `claude -p` は、対話セッションと同じ文脈を読み込む。作業ディレクトリの設定と、ユーザアカウントの `~/.claude` の設定の両方が対象になる | 同上: "Without it, `claude -p` loads the same context an interactive session would, including anything configured in the working directory or `~/.claude`." | 公式文書 |
| 6d | Agentの最後の応答をJSON Schemaに従わせる機能は、Claude CodeにもCodexにもある。Claude Codeは `claude -p --output-format json --json-schema <schema>` で、結果は `structured_output` に入る。Codexは `codex exec --output-schema <file>` で、`-o` で最後の応答をファイルに書ける | https://code.claude.com/docs/en/headless.md、https://learn.chatgpt.com/docs/non-interactive-mode | 公式文書 |
| 6e | `claude -p` に `--setting-sources project` を付けると、ユーザアカウントの `~/.claude/CLAUDE.md` が読み込まれなくなる。サブスクリプションのログインはそのまま使える | 同じ質問を2回実行した。オプションなしでは `~/.claude/CLAUDE.md` の内容を答え、オプションありでは「その指示はない」と答えた。どちらもサブスクリプションで実行できた (Claude Code 2.1.267) | 実測 |

## 2. GitHub上の身元 (2026-09-19)

| # | 制約 | 根拠 | 確度 |
|---|---|---|---|
| 7 | 無料のmachine accountは、個人アカウントに加えて1つまで。roleごとに4つの無料アカウントを作ることはできない | GitHub利用規約 B.3: "You may maintain no more than one free machine account in addition to your free Personal Account." | 公式文書 |
| 8 | GitHub Appは1つの所有者につき100個まで登録できる。seatを消費しない。privateなAppは所有アカウントにだけインストールできる | docs.github.com: registering-a-github-app、deciding-when-to-build-a-github-app、making-a-github-app-public-or-private | 公式文書 |
| 9 | Appの操作はそのAppの名義になる。installation access tokenは1時間で失効し、発行時にリポジトリと権限を絞れる | docs.github.com: authenticating-as-a-github-app-installation、generating-an-installation-access-token-for-a-github-app | 公式文書 |
| 10 | Issue作成、ラベル、sub-issue、依存関係 (blocked by)、Pull Request作成、レビュー (APPROVE)、mergeは、すべてinstallation tokenで呼べる。mergeに要る権限は `Contents: write` | docs.github.com: permissions-required-for-github-apps | 公式文書 |
| 11 | sub-issueの追加と依存関係の追加は、Issue番号ではなくIssueの `id` を渡す | `POST .../issues/{n}/sub_issues` の `sub_issue_id`、`POST .../issues/{n}/dependencies/blocked_by` の `issue_id` | 公式文書 |
| 12 | Pull Requestの作成者は自分のPull Requestをapproveできない。作成者とレビュー者の身元が同じだとapproveが成立しない | docs.github.com: approving-a-pull-request-with-required-reviews | 公式文書 |
| 13 | rulesetとbranch protectionは、Freeプラン (個人・Organizationとも) では公開リポジトリでしか使えない。非公開リポジトリで使うには個人はPro、OrganizationはTeamが要る | docs.github.com: about-rulesets、about-protected-branches | 公式文書 |
| 14 | rulesetのbypass listにGitHub Appを入れられる。必須status checkは「このAppが出したものだけ有効」と発行元を固定できる | docs.github.com: creating-rulesets-for-a-repository、available-rules-for-rulesets | 公式文書 |
| 15 | ラベルを条件にしたruleは、ruleの一覧に存在しない (「risk/mediumならOwnerの承認が必須」をGitHubだけでは書けない) | docs.github.com: available-rules-for-rulesets に記載なし | 公式文書 (不在の確認) |
| 16 | REST APIの上限は、installation tokenがインストールごとに毎時5,000以上、個人のtokenはユーザーごとに毎時5,000 (全token共有)。内容を作る操作には毎分80・毎時500の二次制限がある | docs.github.com: rate-limits-for-the-rest-api | 公式文書 |
| 17 | GitHub Appのapproveが「必須承認数」に数えられるか | 公式文書に記載なし。コミュニティでは「数えられる」との報告がある | 未確認 |
| 18 | Appの表示名が `<slug>[bot]` になること、コミットのメールアドレスが `<bot-user-id>+<slug>[bot]@users.noreply.github.com` であること | 広く観測されている慣習。公式文書には記載なし | 未確認 |
| 19 | AppのコメントでOwnerを@メンションすると、Ownerに通知が届くこと | 一般の@メンションの規則からの推論 | 未確認 |
| 20 | 必須のcheckになっているGitHub Actionsのjobが、`if` の条件で飛ばされたときは、成功として扱われ、mergeを止めない。workflow全体が飛ばされたとき (パスやブランチの絞り込みなど) は、checkが保留のまま残り、mergeを止める | docs.github.com: troubleshooting-required-status-checks | 公式文書 |
| 21 | ファイルのパスを制限するrule (push ruleset) は、Teamプランの非公開または内部リポジトリでしか使えない。Freeプランの公開リポジトリでは使えない | docs.github.com: about-rulesets | 公式文書 |
| 22 | Issueを作るREST APIには `parent_issue_id` があり、sub-issueを1回の呼び出しで作れる。テンプレートや入力フォームを指定する項目はない。Issueのテンプレートと入力フォームは、画面でIssueを作るときだけ働く | docs.github.com: rest/issues/issues、syntax-for-issue-forms | 公式文書 |
| 23 | レビューを出すREST APIの `event` は `APPROVE`、`REQUEST_CHANGES`、`COMMENT` のどれかで、`REQUEST_CHANGES` と `COMMENT` には本文が要る。レビューのコメントへの返答は、スレッドの最初のコメントに対してだけできる | docs.github.com: rest/pulls/reviews、rest/pulls/comments | 公式文書 |
| 24 | secret scanning、push protection、code scanning、dependency reviewは、公開リポジトリでは無料で使える。Dependabotは全てのプランで使える | docs.github.com: code-security/getting-started/github-security-features | 公式文書 |
| 25 | AgentがAPIで作ったPull Requestに、`.github/pull_request_template.md` が自動で使われるか | 公式文書に記載なし | 未確認 |

## 3. 実装の前に確かめたこと (2026-09-20、Claude Code 2.1.267)

| # | 制約 | 根拠 | 確度 |
|---|---|---|---|
| 26 | `--json-schema` は `--output-format stream-json --verbose` と併用できる。1回の実行で、`rate_limit_event` と、`result` のイベントの `structured_output`、`session_id`、`subtype`、`is_error` が取れる | 公式文書は `--output-format json` との組み合わせしか説明していない。実際に併用して、両方が出力されることを確かめた | 実測 |
| 27 | `--setting-sources project` を付けると、実行の最初に出る `system` / `init` のイベントで、`plugins` と `mcp_servers` が空になり、`skills` は組み込みのものだけになる | ユーザアカウントにplugin、MCPサーバ、skillを入れてあるHostで確かめた | 実測 |
| 28 | `--setting-sources project` を付けても、自動メモリは止まらない。`init` のイベントの `memory_paths.auto` が、ユーザアカウントの下にある、作業ディレクトリごとのメモリを指す。環境変数 `CLAUDE_CODE_DISABLE_AUTO_MEMORY=1` を付けると、`memory_paths` は null になる。設定の `autoMemoryEnabled: false` でも止められる | https://code.claude.com/docs/en/memory.md と、環境変数の有無で `init` のイベントを比べた結果 | 実測 + 公式文書 |
| 29 | `claude -p "/usage"` はモデルを呼ばず (`num_turns` が0、`total_cost_usd` が0)、利用枠を使わない。ただし `rate_limit_event` は出ず、人間向けの文章だけが返る。使用率を機械可読で返すサブコマンドは、`claude --help` にない。機械可読の使用率を読むには、モデルを呼ぶ実行が要る | `rate_limit_event` は、モデルへの要求に対する応答に付いてくる情報である | 実測 |
| 30 | システムプロンプトを `--system-prompt` で短いものに置き換え、1語だけ答えさせる最小の実行でも、`rate_limit_event` は出る。実行は1〜2秒で終わる。入力のほとんどは、CLIが毎回送る定型の部分である | 同左 | 実測 |
| 31 | `-p` でも `--resume <session_id>` でセッションを再開できる。2.1.223以降は、別のディレクトリからでも再開できる | https://code.claude.com/docs/en/sessions.md | 公式文書 |
| 32 | `--max-turns` は、手元の `claude --help` に出てこない。実行時間の上限は、起動する側で持つ必要がある。`-p` の実行は、SIGTERMを受けると終了コード143で終わる | https://code.claude.com/docs/en/headless.md、手元の `--help` | 公式文書 |
| 33 | installation access tokenの期限は、発行から1時間で固定である。発行のAPIで指定できるのは `repositories`、`repository_ids`、`permissions` だけで、期限を変える項目はない | docs.github.com: rest/apps/apps | 公式文書 |
| 34 | `GET /repos/{owner}/{repo}/rules/branches/{branch}` は、installation tokenで呼べる。要る権限は Metadata: Read-only である | docs.github.com: permissions-required-for-github-apps | 公式文書 |
| 35 | check runの一覧を読むには Checks: Read-only、commit statusを読むには Commit statuses: Read-only が要る、と書かれている。公開リポジトリなら権限なしで読めるかは、確かめていない | 同上 | 公式文書 (公開リポジトリでの要否は未確認) |
| 36 | GraphQLに、IssueとPull Requestの紐づけを読む項目がある。`Issue.closedByPullRequestsReferences` (既定は開いているPull Requestだけ。`includeClosedPrs` でmerge済みも含む) と、`PullRequest.closingIssuesReferences` である。`Issue.blockedBy`、`Issue.parent`、`Issue.subIssuesSummary` もある。installation tokenで読めるかは、確かめていない | GraphQLのスキーマのintrospection | 実測 (installation tokenでは未確認) |
| 37 | sub-issueの一覧と、blocked by の一覧は、Issueのオブジェクト (ラベルと状態を含む) を返す。親のIssueを返す `GET /repos/{owner}/{repo}/issues/{issue_number}/parent` もある | docs.github.com: rest/issues/sub-issues、rest/issues/issue-dependencies | 公式文書 |
| 38 | GitHub Appは、manifestから登録できる (GitHub App Manifest flow)。名前や権限を書いたmanifestを `https://github.com/organizations/<org>/settings/apps/new` に渡し、人が "Create GitHub App" を押すと、`redirect_url` にcodeが戻る。1時間以内に `POST /app-manifests/{code}/conversions` を呼ぶと、`id` や `pem` (秘密鍵) が返る。`redirect_url` に手元のアドレスを使えるかは、文書に記載がない | docs.github.com: registering-a-github-app-from-a-manifest | 公式文書 (手元のアドレスへのredirectは未確認) |
| 39 | GraphQLのAPIは、installation tokenに、1時間あたり5,000ポイントを割り当てる。同じインストールの対象のリポジトリ全てで、この枠を分け合う。1つの問い合わせは1ポイント以上かかる。`first` と `last` に指定できるのは1から100までである | docs.github.com: rate-limits-and-query-limits-for-the-graphql-api | 公式文書 |
| 40 | GraphQLに、ラベルが付いた時刻と、レビューとcheckの状態を読む項目がある。`LabeledEvent` に `createdAt` と `label`。`Issue.timelineItems` に `itemTypes` と `since`。`PullRequest` に `reviews`、`headRefOid`、`statusCheckRollup`。`PullRequestReview` に `author`、`state`、`commit`、`submittedAt`。installation tokenで読めるかは、確かめていない | GraphQLのスキーマのintrospection (2026-09-20) | 実測 (installation tokenでは未確認) |
| 41 | macOSの `security find-generic-password -s <service> -a <account> -w` は、項目のパスワードだけを出力する。改行を含む値を読むと形が変わる、という報告があるが、確かめていない | `man security` | 公式文書 (改行を含む値は未確認) |
| 42 | GitHub Appが作ったPull Requestでは、作成者の種類 (`pull_request.user.type`) が `Bot` になる見込みである。保護されたパスのcheckは、これを条件に使う | 広く観測されている振る舞い。公式文書には、Appが作ったPull Requestについての明記を見つけていない。使い捨てのリポジトリで確かめる | 未確認 |
| 43 | `GET /apps/{app_slug}` で、privateなGitHub Appを、Organizationの管理者のtokenで読めるかどうか | 公式文書 (rest/apps/apps) に記載がない。保護されたパスのcheckがAppの名前を使わなくなったので、cuminは、この呼び出しに頼らない | 未確認 |
