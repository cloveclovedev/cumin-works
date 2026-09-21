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
| 18 | Appの表示名が `<slug>[bot]` になること、コミットのメールアドレスが `<bot-user-id>+<slug>[bot]@users.noreply.github.com` であること | 広く観測されている慣習。公式文書には記載なし | 未確認。4節の49で実測した |
| 19 | AppのコメントでOwnerを@メンションすると、Ownerに通知が届くこと | 一般の@メンションの規則からの推論 | 未確認。4節の50で実測した |
| 20 | 必須のcheckになっているGitHub Actionsのjobが、`if` の条件で飛ばされたときは、成功として扱われ、mergeを止めない。workflow全体が飛ばされたとき (パスやブランチの絞り込みなど) は、checkが保留のまま残り、mergeを止める | docs.github.com: troubleshooting-required-status-checks | 公式文書。4節の51で実測した |
| 21 | ファイルのパスを制限するrule (push ruleset) は、Teamプランの非公開または内部リポジトリでしか使えない。Freeプランの公開リポジトリでは使えない | docs.github.com: about-rulesets | 公式文書 |
| 22 | Issueを作るREST APIには `parent_issue_id` があり、sub-issueを1回の呼び出しで作れる。テンプレートや入力フォームを指定する項目はない。Issueのテンプレートと入力フォームは、画面でIssueを作るときだけ働く | docs.github.com: rest/issues/issues、syntax-for-issue-forms | 公式文書 |
| 23 | レビューを出すREST APIの `event` は `APPROVE`、`REQUEST_CHANGES`、`COMMENT` のどれかで、`REQUEST_CHANGES` と `COMMENT` には本文が要る。レビューのコメントへの返答は、スレッドの最初のコメントに対してだけできる | docs.github.com: rest/pulls/reviews、rest/pulls/comments | 公式文書 |
| 24 | secret scanning、push protection、code scanning、dependency reviewは、公開リポジトリでは無料で使える。Dependabotは全てのプランで使える | docs.github.com: code-security/getting-started/github-security-features | 公式文書 |
| 25 | AgentがAPIで作ったPull Requestに、`.github/pull_request_template.md` が自動で使われるか | 公式文書に記載なし | 未確認。4節の52で実測した |

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
| 34 | `GET /repos/{owner}/{repo}/rules/branches/{branch}` は、installation tokenで呼べる。要る権限は Metadata: Read-only である | docs.github.com: permissions-required-for-github-apps | 公式文書。4節の53で実測した |
| 35 | check runの一覧を読むには Checks: Read-only、commit statusを読むには Commit statuses: Read-only が要る、と書かれている。公開リポジトリなら権限なしで読めるかは、確かめていない | 同上 | 公式文書 (公開リポジトリでの要否は未確認)。4節の54で実測した |
| 36 | GraphQLに、IssueとPull Requestの紐づけを読む項目がある。`Issue.closedByPullRequestsReferences` (既定は開いているPull Requestだけ。`includeClosedPrs` でmerge済みも含む) と、`PullRequest.closingIssuesReferences` である。`Issue.blockedBy`、`Issue.parent`、`Issue.subIssuesSummary` もある。installation tokenで読めるかは、確かめていない | GraphQLのスキーマのintrospection | 実測 (installation tokenでは未確認)。4節の55で実測した |
| 37 | sub-issueの一覧と、blocked by の一覧は、Issueのオブジェクト (ラベルと状態を含む) を返す。親のIssueを返す `GET /repos/{owner}/{repo}/issues/{issue_number}/parent` もある | docs.github.com: rest/issues/sub-issues、rest/issues/issue-dependencies | 公式文書 |
| 38 | GitHub Appは、manifestから登録できる (GitHub App Manifest flow)。名前や権限を書いたmanifestを `https://github.com/organizations/<org>/settings/apps/new` に渡し、人が "Create GitHub App" を押すと、`redirect_url` にcodeが戻る。1時間以内に `POST /app-manifests/{code}/conversions` を呼ぶと、`id` や `pem` (秘密鍵) が返る。`redirect_url` に手元のアドレスを使えるかは、文書に記載がない | docs.github.com: registering-a-github-app-from-a-manifest | 公式文書 (手元のアドレスへのredirectは未確認)。4節の44で実測した |
| 39 | GraphQLのAPIは、installation tokenに、1時間あたり5,000ポイントを割り当てる。同じインストールの対象のリポジトリ全てで、この枠を分け合う。1つの問い合わせは1ポイント以上かかる。`first` と `last` に指定できるのは1から100までである | docs.github.com: rate-limits-and-query-limits-for-the-graphql-api | 公式文書 |
| 40 | GraphQLに、ラベルが付いた時刻と、レビューとcheckの状態を読む項目がある。`LabeledEvent` に `createdAt` と `label`。`Issue.timelineItems` に `itemTypes` と `since`。`PullRequest` に `reviews`、`headRefOid`、`statusCheckRollup`。`PullRequestReview` に `author`、`state`、`commit`、`submittedAt`。installation tokenで読めるかは、確かめていない | GraphQLのスキーマのintrospection (2026-09-20) | 実測 (installation tokenでは未確認)。4節の55で実測した |
| 41 | macOSの `security find-generic-password -s <service> -a <account> -w` は、項目のパスワードだけを出力する。改行を含む値を読むと形が変わる、という報告があるが、確かめていない | `man security` | 公式文書 (改行を含む値は未確認)。4節の63で実測した |
| 42 | GitHub Appが作ったPull Requestでは、作成者の種類 (`pull_request.user.type`) が `Bot` になる見込みである。保護されたパスのcheckは、これを条件に使う | 広く観測されている振る舞い。公式文書には、Appが作ったPull Requestについての明記を見つけていない。使い捨てのリポジトリで確かめる | 未確認。4節の48で実測した |
| 43 | `GET /apps/{app_slug}` で、privateなGitHub Appを、Organizationの管理者のtokenで読めるかどうか | 公式文書 (rest/apps/apps) に記載がない。保護されたパスのcheckがAppの名前を使わなくなったので、cuminは、この呼び出しに頼らない | 未確認。4節の47で実測した |

## 4. 使い捨てのリポジトリと、Hostで確かめたこと (2026-09-20、2026-09-21)

公開の使い捨てのリポジトリに、roleごとの4つのGitHub Appを登録して確かめた。確度は、断りのない限り実測である。「答えた行」は、この文書の前の節で未確認だった行である。

| # | 制約 | 答えた行 |
|---|---|---|
| 44 | GitHub App Manifest flowは、`redirect_url` に `http://127.0.0.1:<port>` を受け付ける。`hook_attributes` のないmanifestも受け付ける | 38 |
| 45 | Manifest flowでAppが登録されるのは、人が "Create GitHub App" を押したときではなく、codeを交換したとき (`POST /app-manifests/{code}/conversions`) である。交換の前に止まった流れは、Appを残さない | — |
| 46 | 登録されたAppの権限は、manifestに書いた権限に、GitHubが自分で足す `metadata: read` を加えたものになる | — |
| 47 | Organizationのownerは、自分のtokenで、privateなAppを `GET /apps/{slug}` で読める。認証なしでは404が返る | 43 |
| 48 | Appが作ったPull Requestの作成者は、`user.type` が `Bot` になる。APIでも、workflowの中の `github.event.pull_request.user.type` でも同じである | 42 |
| 49 | Appのloginは `<slug>[bot]` である。メールアドレスが `<botのuser id>+<slug>[bot]@users.noreply.github.com` のコミットは、そのbotのユーザに結び付く | 18 |
| 50 | Appが、人を@メンションするコメントを投稿できる (201)。通知が届くことを、Ownerが2026-09-21に確かめた | 19 |
| 51 | `if` の条件で飛ばされたjobのcheck runは、`status: completed`、`conclusion: skipped` になる。必須のcheckであっても、mergeを止めない。cuminは、必須のcheckの `skipped` を、通ったものとして数える必要がある | 20 |
| 52 | AppがAPIで作ったPull Requestには、`.github/pull_request_template.md` が使われない。本文を渡さなければ、本文は空になる | 25 |
| 53 | `GET /repos/{owner}/{repo}/rules/branches/{branch}` は、installation tokenで呼べて、必須のcheckの一覧が返る | 34 |
| 54 | 公開リポジトリでは、installation tokenは、Checks、Commit statuses、Actions の権限がなくても、check run、commit status、check runのannotation、jobのログを読める。jobのログは、認証なしでは読めない (403)。jobのIDは、check runの `details_url` の最後の部分である。失敗したGitHub Actionsのcheck runは、`output.title` が空で、内容はannotationに入る | 35 |
| 55 | installation tokenで、設計メモが使うGraphQLの項目を全て読める。`Issue.closedByPullRequestsReferences(includeClosedPrs: true)`、`Issue.blockedBy`、`Issue.parent`、`Issue.subIssuesSummary`、`PullRequest.closingIssuesReferences`、`PullRequest.statusCheckRollup` | 36、40 |
| 56 | `POST /repos/{owner}/{repo}/issues` に `parent_issue_id` を渡すと、Appのtokenでも、1回の呼び出しでsub-issueを作れる。blocked by の追加は201を返す | 11、22 |
| 57 | Appより狭い権限で発行したtokenは、その外の操作を拒否される (403)。Appが持っていない権限を求めると、発行が422で失敗する | 9、33 |
| 58 | 公開リポジトリでは、Issuesが読み取りだけのtokenでも、Issueを作り、コメントを書ける (201)。GitHubのアカウントなら誰でもできる操作だからである。Issuesの権限で止められるのは、ラベルの付け替え、本文の編集、Issueを閉じることなどである | — |
| 59 | レビューの一覧 (`GET /pulls/{n}/reviews`) では、`REQUEST_CHANGES` と `APPROVE` のレビューの `state` が、`CHANGES_REQUESTED` と `APPROVED` になる。`commit_id` は、レビューしたコミットの完全なSHAである | 23 |
| 60 | Appが作った、本文に `Closes #N` のあるPull Requestを、別のAppがmergeすると、sub-issueである #N が数秒で閉じる | — |
| 61 | cumin-coreのAppは、今の権限のままで、ImplementerのAppが作ったPull Requestにラベルを付け、外せる | — |
| 62 | "Restrict updates" のruleがあるブランチへのPull Requestは、mergeの状態が常に `blocked` になる (`mergeable_state`、GraphQLでは `BLOCKED`)。bypass listにいる相手から見ても、必須のcheckが全て通っていても、同じである。それでも、bypass listにいる相手のmergeの呼び出しは成功する (200)。bypass listにいないAppは、405 (`Repository rule violations found`) を受け取る。`gh pr merge` には `--admin` が要る。cuminは、`clean` になるのを待ってはいけない | 14 |
| 63 | `security find-generic-password -w` は、改行を含む値を16進で返す。1行の値は、そのまま返す | 41 |
| 64 | `security -i` は、コマンドを標準入力から読む。秘密の値を、引数に出さずに渡せる。失敗したコマンドの終了コードを返す。項目がないときの終了コードは44である。保存に失敗しても、値を出力しない | — |
| 65 | `security add-generic-password` に、存在しないkeychainのファイルのパスを渡すと、成功を報告して、既定のkeychainに書く | — |
| 66 | keychainを指定しない `find-generic-password` と `delete-generic-password` は、検索の一覧にある全てのkeychainを探す。`security default-keychain` は、既定のkeychainのパスを出力する | — |
| 67 | launchdが起動したプロセス (ログイン中のユーザのLaunchAgent) は、`security` が作ったKeychainの項目を、確認のダイアログなしで `security` から読める | — |
| 68 | rulesetのbypassの相手の種類 `RepositoryRole` で、`actor_id: 5` は `admin` のroleである。同じ内容でrulesetを `PUT` しても、履歴の版は増えない。OAuthのtoken (`gh`) でworkflowのファイルをpushするには、`workflow` のscopeが要る。既定のブランチのworkflowを変えたあと、開いているPull Requestを閉じて開き直しても、古いworkflowが動く。"Update branch" か新しいコミットで、新しいworkflowが動く | — |
| 69 | リポジトリをOrganizationに移しても、Issue、Pull Request、webhook、secret、releaseは保たれ、古いアドレスは転送される | — (公式文書。実測していない) |

## 5. Agentの起動の実装で確かめたこと (2026-09-20、Claude Code 2.1.267、Apple Git 2.50.1)

要求Issue #7 の実装 (#45、#49、#51) で確かめた事実。

| # | 制約 | 根拠 | 確度 |
|---|---|---|---|
| 70 | `git worktree add <path> <branch>` は、`<branch>` がローカルになく、ちょうど1つのリモートにあれば、`git worktree add --track -b <branch> <path> <remote>/<branch>` と同じに扱われる | git-worktree: "If <commit-ish> is a branch name ... and is not found ... but there does exist a tracking branch in exactly one remote ... treat as equivalent to: git worktree add --track -b <branch> <path> <remote>/<branch>" | 公式文書 |
| 71 | `git clone --no-checkout` は、remote-tracking branch と `remote.origin.fetch` を作る。`--bare` はどちらも作らない | git-clone: `--no-checkout` は "Do not checkout HEAD after the clone is complete"。`--bare` は "neither remote-tracking branches nor the related configuration variables are created" | 公式文書 |
| 72 | `git fetch` は `refs/remotes/origin/HEAD` を動かさない。`git remote set-head origin --auto` がリモートに問い合わせて、`refs/remotes/origin/HEAD` をリモートの既定のブランチに向ける | git-remote: "With -a or --auto, the remote is queried to determine its HEAD, then the symbolic-ref refs/remotes/<name>/HEAD is set to the same branch"。git-fetch には HEAD の更新の記述がない | 公式文書 |
| 73 | `git worktree list --porcelain` は、worktreeの実体のパスを出す。macOSでは、`/var` の下の一時ディレクトリが `/private/var` で出る | テストで `filepath.EvalSymlinks` と比べた | 実測 |
| 74 | `claude -p` は、標準入力が開いたまま何も来ないと、3秒待ってから進み、標準エラー出力に "Warning: no stdin data received in 3s, proceeding without it" を出す。標準入力がnullデバイスなら待たない | 最小の実行で観測した。cuminは標準入力をnullデバイスにして起動する | 実測 |
| 75 | `rate_limit_event` には、`rate_limit_info.unifiedWindows` (1を参照) のほかに、`session_id` と `uuid` があり、`rate_limit_info` には `status`、`resetsAt`、`rateLimitType`、overageの項目がある。正常終了の `result` のイベントには、`subtype: "success"`、`is_error: false`、`structured_output` (26を参照) のほかに、`terminal_reason`、`stop_reason`、`permission_denials` がある | 最小の実行の出力の項目名を確かめた。値は記録していない | 実測 |
| 76 | Bashで `sleep 600` を実行中の `claude -p` のプロセスグループにSIGTERMを送ると、CLIは猶予を待たずに終わり、プロセスグループに何も残らない (32の続き) | #42 の実機の確認。打ち切りのあとに `pgrep -g <プロセスグループ>` が何も返さなかった | 実測 |

## 6. 定期確認の実装で確かめたこと (2026-09-21)

要求Issue #6 の実装 (#71、#75、#77、#78) と、sandboxでの組み込んだバイナリの確認で確かめた事実。

| # | 制約 | 根拠 | 確度 |
|---|---|---|---|
| 77 | GraphQLの問い合わせのポイントは、経路に沿った `first` の積を100で割った値で決まる。定期確認の問い合わせ (要求Issueを10件ずつ、sub-issueを30件、ラベルを10件、blocked by のIssueを20件) は、`rateLimit.cost` が6だった | 公式: Rate limits and node limits for the GraphQL API。開いている要求Issueが4つあるリポジトリで実測 | 公式文書 + 実測 |
| 78 | `PUT /repos/{owner}/{repo}/issues/{n}/labels` に、リポジトリにないラベルの名前を渡すと、そのラベルが既定の色 (`ededed`) で作られ、付け替えは失敗しない | sandboxで `cumin/status/implementing` のラベルを消してから、着手させた | 実測 |

## 7. setupの道具とCIの実装で確かめたこと (2026-09-21)

要求Issue #59 の実装 (#84、#91) で確かめた事実。

| # | 制約 | 根拠 | 確度 |
|---|---|---|---|
| 79 | 公開リポジトリでは、標準のGitHub-hosted runner (macOSを含む) の利用は無料である。Freeプランの同時実行は、全体で20 job、macOSは5 jobまで。`macos-latest` はarm64である | 公式: About billing for GitHub Actions、Usage limits for GitHub Actions、GitHub-hosted runners reference | 公式文書 |
| 80 | `POST /repos/{owner}/{repo}/rulesets` に、そのリポジトリにある ruleset と同じ名前を渡すと、422 "Name must be unique" が返る。同じリポジトリに、同じ名前の ruleset は2つ作れない | sandboxで実測 | 実測 |
| 81 | `GET /repos/{owner}/{repo}/rulesets` は、`includes_parents` (初期値 `true`) により、Organizationの ruleset のうちそのリポジトリに当たるものも返す | 公式: Get all repository rulesets | 公式文書 |
| 82 | Organizationの階層の ruleset は、GitHub Enterprise プランでだけ作れる。Free と Team の Organization では作れないので、Organizationの ruleset とリポジトリの ruleset の名前が重なることは、これらのプランでは起きない | 公式: About rulesets ("For organizations on the GitHub Enterprise plan, you can set up rulesets at the organization level")。Organizationの ruleset の名前の一意性は、公式文書に記載がない | 公式文書 (名前の一意性は未確認) |

## 8. Agentの隔離の実装で確かめたこと (2026-09-21、2026-09-22、Claude Code 2.1.267、git 2.50.1、gh 2.101.0)

要求Issue #8 の実装 (#74、#76、#82、#93) で確かめた事実。

| # | 制約 | 根拠 | 確度 |
|---|---|---|---|
| 83 | `GIT_CONFIG_GLOBAL=/dev/null` と `GIT_CONFIG_NOSYSTEM=1` を付けても、`GIT_AUTHOR_*` と `GIT_COMMITTER_*` がなければ、gitはユーザ名とホスト名から推測した作者でコミットに成功する。cuminは、作者を環境変数で渡す | #74 で実測 | 実測 |
| 84 | `GH_CONFIG_DIR` に存在しないディレクトリを指定し、`GH_TOKEN` を渡すと、ghは既定の設定 (`git_protocol` は `https`) で動き、ディレクトリを作らない | #74 で実測 | 実測 |
| 85 | `CLAUDE_CODE_SUBPROCESS_ENV_SCRUB=1` は、Bashツール、hook、MCPサーバの環境から認証情報を取り除く。cuminは使わない。AgentがBashツールでroleのtokenを使うためである | 公式: Environment variables | 公式文書 |
| 86 | `CLAUDE_CODE_DISABLE_AUTO_MEMORY=1` を付けると、`init` のイベントに `memory_paths` の項目そのものがない (28は null と書いたが、項目がない)。`init` には、指示のファイルの一覧を返す項目がない。ある項目は `plugins`、`mcp_servers`、`skills`、`agents`、`slash_commands`、`tools` など | #76 で実測。項目の全体は #67 に記録 | 実測 |
| 87 | `rate_limit_info` の項目は `status`、`resetsAt`、`rateLimitType`、`unifiedWindows`、`isUsingOverage`、`overageStatus`、`overageDisabledReason` である。通常の実行では `status` は `allowed` になる (75の続き) | #76 で実測 | 実測 |
| 88 | Agent SDKの文書は `SDKRateLimitEvent` を記述しており、`rate_limit_info` の `status`、`utilization`、`resetsAt`、`rateLimitType` (`five_hour`、`seven_day`、`seven_day_opus`、`seven_day_sonnet`、`overage`) がある。イベントは利用枠の状態が変わったときに出る、とある。`unifiedWindows` は記述がない (2の更新) | 公式: Agent SDK のTypeScriptリファレンス (2026-09-21) | 公式文書 |
| 89 | CLIのサブコマンド、フラグ、hook、statuslineの項目、SDKの呼び出しのどれも、モデルを呼ばずにサブスクリプションの使用率を返さない。`/usage` は文章を返し、文書にないendpointをログインのOAuthのtokenで呼んでいる | 公式文書 (2026-09-21) と公開の報告 | 公式文書 (不在の確認) |
| 90 | `GET /users/{username}` は、installation tokenを `Authorization: Bearer` で渡しても、botのユーザ (`<slug>[bot]`) の数値の `id` を返す | 公式: Get a user。#70 の実機の確認で実測 | 公式文書 + 実測 |
| 91 | `--setting-sources project` を付けた `-p` の実行でも、claude.aiのアカウントのコネクタが `init` の `mcp_servers` に現れる。worktreeに `.mcp.json` がなくても同じである。`ENABLE_CLAUDEAI_MCP_SERVERS=false` を付けると消える (27の追記) | #93 で実測 (2026-09-22) | 実測 |
| 92 | Hostのユーザの設定ファイルを読ませず、設計メモの環境変数だけ (tokenを `extraheader` と `GH_TOKEN` で、botの身元を作者とコミッターで) を渡した環境で、`git push` と `gh pr create` はImplementerのAppの名義で成功し、コミットの作者とコミッターはbotのユーザになる | sandboxで実測 (2026-09-22) | 実測 |
