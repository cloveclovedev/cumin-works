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
