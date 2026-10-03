# 調査・実測で確定した制約

Claude Code と GitHub について、公式文書と実機で確かめた事実だけを集める。設計上の決定は含まない。要件の文書ではなく、事実の記録である。行は主題ごとの9つの表に分かれ、表の中では番号の順に並ぶ。番号は確かめた順に付けたもので、文書の全体で一意である。1つの事実は1つの行に書く。まだ確かめていないことと、ある日に観測しただけで今も成り立つか分からないことは、主題の表に置かず、「未確認 (Not confirmed)」の節に集める。この節の行を、事実として読んではいけない。別の行にまとめてなくした番号は、最後の「引退した番号」の表にある。

確度の凡例: 実測 = このホストで実際に動かして観測した、公式文書 = 公式ドキュメントで確認した、未確認 = 公式文書に記載が見つからず、まだ試していない。または、ある日に観測したが、測り直していない。未確認の行は「未確認 (Not confirmed)」の節にだけある。「日付と版」の列は、その行を確かめた日付と、そのときの道具の版である。1つの番号の確かめた部分と確かめていない部分は、同じ番号で主題の表とこの節に分かれ、互いを指す。

## Claude Code

| # | 制約 | 根拠 | 確度 | 日付と版 |
|---|---|---|---|---|
| 1 | headless実行 (`claude -p ... --output-format stream-json --verbose`) の出力に `rate_limit_event` が含まれ、5h枠とweekly枠の使用率とリセット時刻を機械可読で読める | `rate_limit_info.unifiedWindows.five_hour` と `seven_day` のそれぞれに、`utilization` (0から1の使用率) と `resetsAt` (Unix秒) がある。`status: "allowed"` も返る。同時刻の `/usage` の表示と値が一致した | 実測 | 2026-09-19、Claude Code 2.1.267 |
| 2 | `rate_limit_event` は、Agent SDKの文書に `SDKRateLimitEvent` として記述がある。`rate_limit_info` の `status`、`utilization`、`resetsAt`、`rateLimitType` (`five_hour`、`seven_day`、`seven_day_opus`、`seven_day_sonnet`、`overage`) があり、イベントは利用枠の状態が変わったときに出る、とある。`unifiedWindows` は記述がない。headlessの文書には、このイベントの記述がない。バージョンアップで変わりうる | 公式: Agent SDK のTypeScriptリファレンス (2026-09-21)、headless | 公式文書 | 2026-09-21、2026-09-22、Claude Code 2.1.267、git 2.50.1、gh 2.101.0 |
| 3 | `claude -p "/usage"` はheadlessで動き、使用率とリセット時刻を人間向けテキストで返す。`--output-format json` でもテキストが `result` に入るだけ | 出力例: `Current session: <N>% used · resets <日時> (<タイムゾーン>)` | 実測 + 公式文書 | 2026-09-19、Claude Code 2.1.267 |
| 4 | statuslineスクリプトへのJSONには `rate_limits.five_hour.used_percentage` と `resets_at` (Unix秒) などがある。Pro/Max加入者のみ、セッション内の最初のAPI応答後にだけ現れる | https://code.claude.com/docs/en/statusline.md | 公式文書 | 2026-09-19、Claude Code 2.1.267 |
| 5 | Anthropic APIのRate Limits API、Usage & Cost APIはAPI組織向けで、サブスクリプションの5h枠・weekly枠は返さない | https://platform.claude.com/docs/en/manage-claude/rate-limits-api.md | 公式文書 | 2026-09-19、Claude Code 2.1.267 |
| 6a | `claude -p` の `--bare` は、hooks、skills、plugins、MCPサーバ、自動メモリ、CLAUDE.md の自動読み込みを全て省く。ただしサブスクリプションのログインを使えず、`ANTHROPIC_API_KEY` などが要る | https://code.claude.com/docs/en/headless.md: "bare mode doesn't use your subscription login"、"In bare mode, Claude Code never reads OAuth credentials or the system keychain." | 公式文書 | 2026-09-19、Claude Code 2.1.267 |
| 6b | `--bare` は将来 `-p` の既定になる予定と書かれている。そうなったとき、サブスクリプションのログインでheadless実行を続ける方法を確かめる必要がある | 同上: "`--bare` is the recommended mode for scripted and SDK calls, and will become the default for `-p` in a future release." | 公式文書 | 2026-09-19、Claude Code 2.1.267 |
| 6c | `--bare` なしの `claude -p` は、対話セッションと同じ文脈を読み込む。作業ディレクトリの設定と、ユーザアカウントの `~/.claude` の設定の両方が対象になる | 同上: "Without it, `claude -p` loads the same context an interactive session would, including anything configured in the working directory or `~/.claude`." | 公式文書 | 2026-09-19、Claude Code 2.1.267 |
| 6d | Agentの最後の応答をJSON Schemaに従わせる機能は、Claude CodeにもCodexにもある。Claude Codeは `claude -p --output-format json --json-schema <schema>` で、結果は `structured_output` に入る。Codexは `codex exec --output-schema <file>` で、`-o` で最後の応答をファイルに書ける | https://code.claude.com/docs/en/headless.md、https://learn.chatgpt.com/docs/non-interactive-mode | 公式文書 | 2026-09-19、Claude Code 2.1.267 |
| 6e | `claude -p` に `--setting-sources project` を付けると、ユーザアカウントの `~/.claude/CLAUDE.md` が読み込まれなくなる。サブスクリプションのログインはそのまま使える | 同じ質問を2回実行した。オプションなしでは `~/.claude/CLAUDE.md` の内容を答え、オプションありでは「その指示はない」と答えた。どちらもサブスクリプションで実行できた (Claude Code 2.1.267) | 実測 | 2026-09-19、Claude Code 2.1.267 |
| 26 | `--json-schema` は `--output-format stream-json --verbose` と併用できる。1回の実行で、`rate_limit_event` と、`result` のイベントの `structured_output`、`session_id`、`subtype`、`is_error` が取れる | 公式文書は `--output-format json` との組み合わせしか説明していない。実際に併用して、両方が出力されることを確かめた | 実測 | 2026-09-20、Claude Code 2.1.267 |
| 27 | `--setting-sources project` を付けると、実行の最初に出る `system` / `init` のイベントで、`skills` は組み込みのものだけになる。`plugins` に載るのは、バイナリに入ったpluginだけである (107を参照)。`mcp_servers` は、claude.aiのアカウントのコネクタを除いて空になる (91を参照) | ユーザアカウントにplugin、MCPサーバ、skillを入れてあるHostで確かめた | 実測 | 2026-09-20、Claude Code 2.1.267 |
| 29 | `claude -p "/usage"` はモデルを呼ばず (`num_turns` が0、`total_cost_usd` が0)、利用枠を使わない。ただし `rate_limit_event` は出ず、人間向けの文章だけが返る。使用率を機械可読で返すサブコマンドは、`claude --help` にない。機械可読の使用率を読むには、モデルを呼ぶ実行が要る。CLIのサブコマンド、フラグ、hook、statuslineの項目、SDKの呼び出しのどれも、モデルを呼ばずにサブスクリプションの使用率を返さない。`/usage` は、文書にないendpointをログインのOAuthのtokenで呼んでいる | `rate_limit_event` は、モデルへの要求に対する応答に付いてくる情報である。公式文書 (2026-09-21) と公開の報告 | 実測 + 公式文書 (不在の確認) | 2026-09-21、2026-09-22、Claude Code 2.1.267、git 2.50.1、gh 2.101.0 |
| 30 | システムプロンプトを `--system-prompt` で短いものに置き換え、1語だけ答えさせる最小の実行でも、`rate_limit_event` は出る。実行は1〜2秒で終わる。入力のほとんどは、CLIが毎回送る定型の部分である | 同左 | 実測 | 2026-09-20、Claude Code 2.1.267 |
| 31 | `-p` でも `--resume <session_id>` でセッションを再開できる。2.1.223以降は、別のディレクトリからでも再開できる | https://code.claude.com/docs/en/sessions.md | 公式文書 | 2026-09-20、Claude Code 2.1.267 |
| 32 | `--max-turns` は、手元の `claude --help` に出てこない。実行時間の上限は、起動する側で持つ必要がある。`-p` の実行は、SIGTERMを受けると終了コード143で終わる。Bashで `sleep 600` を実行中の `claude -p` のプロセスグループにSIGTERMを送ると、CLIは猶予を待たずに終わり、プロセスグループに何も残らない | https://code.claude.com/docs/en/headless.md、手元の `--help`。プロセスグループへのSIGTERMは #42 の実機の確認で、打ち切りのあとに `pgrep -g <プロセスグループ>` が何も返さなかった | 公式文書 + 実測 | 2026-09-20、Claude Code 2.1.267、Apple Git 2.50.1 |
| 74 | `claude -p` は、標準入力が開いたまま何も来ないと、3秒待ってから進み、標準エラー出力に "Warning: no stdin data received in 3s, proceeding without it" を出す。標準入力がnullデバイスなら待たない | 最小の実行で観測した | 実測 | 2026-09-20、Claude Code 2.1.267、Apple Git 2.50.1 |
| 85 | `CLAUDE_CODE_SUBPROCESS_ENV_SCRUB=1` は、Bashツール、hook、MCPサーバの環境から認証情報を取り除く | 公式: Environment variables | 公式文書 | 2026-09-21、2026-09-22、Claude Code 2.1.267、git 2.50.1、gh 2.101.0 |
| 86 | `--setting-sources project` を付けても、自動メモリは止まらない。`init` のイベントの `memory_paths.auto` が、ユーザアカウントの下にある、作業ディレクトリごとのメモリを指す。環境変数 `CLAUDE_CODE_DISABLE_AUTO_MEMORY=1` を付けると、`init` のイベントに `memory_paths` の項目そのものがない。設定の `autoMemoryEnabled: false` でも止められる。`init` には、指示のファイルの一覧を返す項目がない。ある項目は `plugins`、`mcp_servers`、`skills`、`agents`、`slash_commands`、`tools` など | https://code.claude.com/docs/en/memory.md。環境変数の有無で `init` のイベントを比べた。#76 で実測。項目の全体は #67 に記録 | 実測 + 公式文書 | 2026-09-21、2026-09-22、Claude Code 2.1.267、git 2.50.1、gh 2.101.0 |
| 87 | `rate_limit_event` には、`rate_limit_info` のほかに、`session_id` と `uuid` がある。`rate_limit_info` の項目は `status`、`resetsAt`、`rateLimitType`、`unifiedWindows` (1を参照)、`isUsingOverage`、`overageStatus`、`overageDisabledReason` である。通常の実行では `status` は `allowed` になる。正常終了の `result` のイベントには、`subtype: "success"`、`is_error: false`、`structured_output` (26を参照) のほかに、`terminal_reason`、`stop_reason`、`permission_denials` がある | 最小の実行の出力の項目名を確かめた。#76 で実測 | 実測 | 2026-09-21、2026-09-22、Claude Code 2.1.267、git 2.50.1、gh 2.101.0 |
| 91 | `--setting-sources project` を付けた `-p` の実行でも、claude.aiのアカウントのコネクタが `init` の `mcp_servers` に現れる。worktreeに `.mcp.json` がなくても同じである。`ENABLE_CLAUDEAI_MCP_SERVERS=false` を付けると消える | #93 で実測 (2026-09-22) | 実測 | 2026-09-21、2026-09-22、Claude Code 2.1.267、git 2.50.1、gh 2.101.0 |
| 97 | `--setting-sources project` と `--add-dir <ディレクトリ>` を付けた `-p` の実行で、Agentに見えるskillは、そのディレクトリのskillと、CLIに組み込みのskillだけである。Hostのユーザの `~/.claude/skills/` のskillは見えない | live scenario Impl-1 (#101) の記録 | 実測 | 2026-09-22、Claude Code 2.1.267 |
| 107 | `--setting-sources project` を付けても、`init` の `plugins` に、バイナリに入ったpluginが載る。2.1.284 では `agents-md@builtin` と `telemetry@builtin` で、`path` は `builtin` である。claude.aiから同期したpluginではないので、`syncClaudeAiPlugins` では消えない | Hostで実測 (2026-09-29)。公式: `anthropics/claude-code` の `mods/` | 実測 + 公式文書 | 2026-09-25、2026-09-29、Claude Code 2.1.284 |
| 108 | `builtin` は予約されたmarketplaceの名前で、Claude Codeはバイナリに入ったpluginにだけ使う。marketplace、claude.ai、skillsのディレクトリから来たpluginの `source` が `<名前>@builtin` になることはない | 公式: Marketplace reference の "Reserved names" (#207 で確認) | 公式文書 | 2026-09-25、2026-09-29、Claude Code 2.1.284 |
| 111 | `init` のイベントの `skills` は、skillの名前の文字列の配列である (86で項目の名前だけを記録した) | #166 の記録 (2026-09-25、Claude Code 2.1.273) | 実測 | 2026-09-25、2026-09-29、Claude Code 2.1.273、2.1.284 |

## GitHub Appとtoken

| # | 制約 | 根拠 | 確度 | 日付と版 |
|---|---|---|---|---|
| 7 | 無料のmachine accountは、個人アカウントに加えて1つまで。roleごとに4つの無料アカウントを作ることはできない | GitHub利用規約 B.3: "You may maintain no more than one free machine account in addition to your free Personal Account." | 公式文書 | 2026-09-19 |
| 8 | GitHub Appは1つの所有者につき100個まで登録できる。seatを消費しない。privateなAppは所有アカウントにだけインストールできる | docs.github.com: registering-a-github-app、deciding-when-to-build-a-github-app、making-a-github-app-public-or-private | 公式文書 | 2026-09-19 |
| 10 | Issue作成、ラベル、sub-issue、依存関係 (blocked by)、Pull Request作成、レビュー (APPROVE)、mergeは、すべてinstallation tokenで呼べる。mergeに要る権限は `Contents: write` | docs.github.com: permissions-required-for-github-apps | 公式文書 | 2026-09-19 |
| 19 | Appは、人を@メンションするコメントを投稿できる (201)。メンションされた人に通知が届く | 公開の使い捨てのリポジトリに、roleごとの4つのGitHub Appを登録して確かめた。通知が届くことは、Ownerが2026-09-21に確かめた | 実測 | 2026-09-20、2026-09-21 |
| 33 | installation access tokenの期限は、発行から1時間で固定である。発行のAPIで指定できるのは `repositories`、`repository_ids`、`permissions` だけで、期限を変える項目はない | docs.github.com: rest/apps/apps | 公式文書 | 2026-09-20、Claude Code 2.1.267 |
| 44 | GitHub Appは、manifestから登録できる (GitHub App Manifest flow)。名前や権限を書いたmanifestを `https://github.com/organizations/<org>/settings/apps/new` に渡し、人が "Create GitHub App" を押すと、`redirect_url` にcodeが戻る。1時間以内に `POST /app-manifests/{code}/conversions` を呼ぶと、`id` や `pem` (秘密鍵) が返る。`redirect_url` に `http://127.0.0.1:<port>` を受け付ける。`hook_attributes` のないmanifestも受け付ける | docs.github.com: registering-a-github-app-from-a-manifest。公開の使い捨てのリポジトリに、roleごとの4つのGitHub Appを登録して確かめた | 公式文書 + 実測 | 2026-09-20、2026-09-21 |
| 45 | Manifest flowでAppが登録されるのは、人が "Create GitHub App" を押したときではなく、codeを交換したとき (`POST /app-manifests/{code}/conversions`) である。交換の前に止まった流れは、Appを残さない | 公開の使い捨てのリポジトリに、roleごとの4つのGitHub Appを登録して確かめた | 実測 | 2026-09-20、2026-09-21 |
| 46 | 登録されたAppの権限は、manifestに書いた権限に、GitHubが自分で足す `metadata: read` を加えたものになる | 公開の使い捨てのリポジトリに、roleごとの4つのGitHub Appを登録して確かめた | 実測 | 2026-09-20、2026-09-21 |
| 47 | Organizationのownerは、自分のtokenで、privateなAppを `GET /apps/{slug}` で読める。認証なしでは404が返る | 公開の使い捨てのリポジトリに、roleごとの4つのGitHub Appを登録して確かめた | 実測 | 2026-09-20、2026-09-21 |
| 48 | Appが作ったPull Requestの作成者は、`user.type` が `Bot` になる。APIでも、workflowの中の `github.event.pull_request.user.type` でも同じである | 公開の使い捨てのリポジトリに、roleごとの4つのGitHub Appを登録して確かめた | 実測 | 2026-09-20、2026-09-21 |
| 49 | Appの操作は、そのAppの名義になる。Appのloginは `<slug>[bot]` である。メールアドレスが `<botのuser id>+<slug>[bot]@users.noreply.github.com` のコミットは、そのbotのユーザに結び付く | docs.github.com: authenticating-as-a-github-app-installation。公開の使い捨てのリポジトリに、roleごとの4つのGitHub Appを登録して確かめた | 公式文書 + 実測 | 2026-09-20、2026-09-21 |
| 57 | Appより狭い権限で発行したtokenは、その外の操作を拒否される (403)。Appが持っていない権限を求めると、発行が422で失敗する | 公開の使い捨てのリポジトリに、roleごとの4つのGitHub Appを登録して確かめた | 実測 | 2026-09-20、2026-09-21 |
| 58 | 公開リポジトリでは、Issuesが読み取りだけのtokenでも、Issueを作り、コメントを書ける (201)。GitHubのアカウントなら誰でもできる操作だからである。Issuesの権限で止められるのは、ラベルの付け替え、本文の編集、Issueを閉じることなどである | 公開の使い捨てのリポジトリに、roleごとの4つのGitHub Appを登録して確かめた | 実測 | 2026-09-20、2026-09-21 |
| 61 | cumin-coreのAppは、今の権限のままで、ImplementerのAppが作ったPull Requestにラベルを付け、外せる | 公開の使い捨てのリポジトリに、roleごとの4つのGitHub Appを登録して確かめた | 実測 | 2026-09-20、2026-09-21 |
| 84 | `GH_CONFIG_DIR` に存在しないディレクトリを指定し、`GH_TOKEN` を渡すと、ghは既定の設定 (`git_protocol` は `https`) で動き、ディレクトリを作らない | #74 で実測 | 実測 | 2026-09-21、2026-09-22、Claude Code 2.1.267、git 2.50.1、gh 2.101.0 |
| 92 | Hostのユーザの設定ファイルを読ませず、設計メモの環境変数だけ (tokenを `extraheader` と `GH_TOKEN` で、botの身元を作者とコミッターで) を渡した環境で、`git push` と `gh pr create` はImplementerのAppの名義で成功し、コミットの作者とコミッターはbotのユーザになる | sandboxで実測 (2026-09-22) | 実測 | 2026-09-21、2026-09-22、Claude Code 2.1.267、git 2.50.1、gh 2.101.0 |
| 110 | GitHub Appの名前を変えると、slugが名前に追従する。新しいslugの `GET /apps/{slug}` が答え、古いslugは404になる。App idは変わらない。Organizationへのインストールも、選んだリポジトリも、そのまま残る。botのログイン名はslugに追従するので、改名のあとのコミットは新しい名前になり、前のコミットは古い名前のまま残る | Ownerが改名した直後にRESTで確かめた (2026-09-25、#155 のNote)。公式文書は、改名で何が残るかを書いていない | 実測 | 2026-09-25、2026-09-29、Claude Code 2.1.273、2.1.284 |
| 112 | Issues の書き込みだけを持ち、Pull requests の権限を持たないAppのinstallation tokenで、sub-issueの作成、マイルストーンと blocked by の設定、コメントができる。`GET /repos/{owner}/{repo}/issues/{number}` で、Pull Requestの本文も読める | 公式: Permissions required for GitHub Apps (Issues の項にこのendpointがある)。live scenario Accept-1 (#172) で実測 (2026-09-29、Claude Code 2.1.284) | 公式文書 + 実測 | 2026-09-25、2026-09-29、Claude Code 2.1.273、2.1.284 |
| 114 | 画面でIssueに画像を添付するときのアップロード (`user-attachments`) は、GitHub Appのinstallation tokenを受け付けない。Contentsの書き込みを持つAppのtokenでも、SVGとPNGのどちらも、`token` と `Bearer` のどちらの形でも404になる。`gh` の添付の機能は、installation tokenでは送る前に拒否する。REST APIの文書にこのendpointはない | sandboxで実測 (2026-09-29、#198 の decision request) | 実測 | 2026-09-29、2026-09-30 |
| 126 | GitHub Appのインストールにリポジトリを足す `PUT /user/installations/{installation_id}/repositories/{repository_id}` は、`gh` のログインのtoken (OAuth、`gho_`) では、4つのAppのどれでも404になる。`GET /user/installations` は403で、「GitHub Appに認可されたtokenで認証する」よう求められる。公式文書は、このendpointを classic personal access token (`repo` の scope) でだけ使えるとしている | 公式: REST API endpoints for GitHub App installations ("Add a repository to an app installation")。Hostで実測 | 公式文書 + 実測 | 2026-10-02 |

## REST API

| # | 制約 | 根拠 | 確度 | 日付と版 |
|---|---|---|---|---|
| 16 | REST APIの上限は、installation tokenがインストールごとに毎時5,000以上、個人のtokenはユーザーごとに毎時5,000 (全token共有)。内容を作る操作には毎分80・毎時500の二次制限がある | docs.github.com: rate-limits-for-the-rest-api | 公式文書 | 2026-09-19 |
| 37 | sub-issueの一覧と、blocked by の一覧は、Issueのオブジェクト (ラベルと状態を含む) を返す。親のIssueを返す `GET /repos/{owner}/{repo}/issues/{issue_number}/parent` もある | docs.github.com: rest/issues/sub-issues、rest/issues/issue-dependencies | 公式文書 | 2026-09-20、Claude Code 2.1.267 |
| 52 | AppがAPIで作ったPull Requestには、`.github/pull_request_template.md` が使われない。本文を渡さなければ、本文は空になる | 公開の使い捨てのリポジトリに、roleごとの4つのGitHub Appを登録して確かめた | 実測 | 2026-09-20、2026-09-21 |
| 56 | `POST /repos/{owner}/{repo}/issues` に `parent_issue_id` を渡すと、Appのtokenでも、1回の呼び出しでsub-issueを作れる。blocked by の追加は201を返す。sub-issueの追加と blocked by の追加は、Issue番号ではなくIssueの `id` を渡す (`POST .../issues/{n}/sub_issues` の `sub_issue_id`、`POST .../issues/{n}/dependencies/blocked_by` の `issue_id`)。Issueを作るREST APIに、テンプレートや入力フォームを指定する項目はない。Issueのテンプレートと入力フォームは、画面でIssueを作るときだけ働く | docs.github.com: rest/issues/issues、rest/issues/sub-issues、rest/issues/issue-dependencies、syntax-for-issue-forms。公開の使い捨てのリポジトリに、roleごとの4つのGitHub Appを登録して確かめた | 公式文書 + 実測 | 2026-09-20、2026-09-21 |
| 59 | レビューを出すREST APIの `event` は `APPROVE`、`REQUEST_CHANGES`、`COMMENT` のどれかで、`REQUEST_CHANGES` と `COMMENT` には本文が要る。レビューのコメントへの返答は、スレッドの最初のコメントに対してだけできる。レビューの一覧 (`GET /pulls/{n}/reviews`) では、`REQUEST_CHANGES` と `APPROVE` のレビューの `state` が、`CHANGES_REQUESTED` と `APPROVED` になる。`commit_id` は、レビューしたコミットの完全なSHAである | docs.github.com: rest/pulls/reviews、rest/pulls/comments。公開の使い捨てのリポジトリに、roleごとの4つのGitHub Appを登録して確かめた | 公式文書 + 実測 | 2026-09-20、2026-09-21 |
| 69 | リポジトリをOrganizationに移しても、Issue、Pull Request、webhook、secret、releaseは保たれ、古いアドレスは転送される | 公式文書による。実測していない | 公式文書 | 2026-09-20、2026-09-21 |
| 78 | `PUT /repos/{owner}/{repo}/issues/{n}/labels` に、リポジトリにないラベルの名前を渡すと、そのラベルが既定の色 (`ededed`) で作られ、付け替えは失敗しない | sandboxで `cumin/status/implementing` のラベルを消してから、着手させた | 実測 | 2026-09-21 |
| 90 | `GET /users/{username}` は、installation tokenを `Authorization: Bearer` で渡しても、botのユーザ (`<slug>[bot]`) の数値の `id` を返す | 公式: Get a user。#70 の実機の確認で実測 | 公式文書 + 実測 | 2026-09-21、2026-09-22、Claude Code 2.1.267、git 2.50.1、gh 2.101.0 |
| 93 | `POST /repos/{owner}/{repo}/issues/{n}/dependencies/blocked_by` の応答は、依存する側 (パスの `{n}`) のIssueであり、依存先のIssueではない | #9 のsub-issueに blocked by を張ったときに観測 | 実測 | 2026-09-22、Claude Code 2.1.267 |
| 99 | `GET /repos/{owner}/{repo}/contents/{path}` を installation token で呼ぶには、Contents の read が要る | 公式: Permissions required for GitHub Apps | 公式文書 | 2026-09-22 |
| 122 | cumin-coreのinstallation tokenで、`GET /repos/{owner}/{repo}/collaborators/{username}/permission` を呼べ、どのアカウントの権限も読める。人のアカウントは `User`、botは `Bot` と分かる。公開リポジトリでは、協力者でない人も `read` と答える | 公式: Get repository permissions for a user (Metadata の読み取り)。#293 の M1 | 公式文書 + 実測 | 2026-10-01 |

## GraphQLとそのポイント

| # | 制約 | 根拠 | 確度 | 日付と版 |
|---|---|---|---|---|
| 39 | GraphQLのAPIは、installation tokenに、1時間あたり5,000ポイントを割り当てる。同じインストールの対象のリポジトリ全てで、この枠を分け合う。1つの問い合わせは1ポイント以上かかる。`first` と `last` に指定できるのは1から100までである | docs.github.com: rate-limits-and-query-limits-for-the-graphql-api | 公式文書 | 2026-09-20、Claude Code 2.1.267 |
| 55 | installation tokenで、設計メモが使うGraphQLの項目を全て読める。`Issue.closedByPullRequestsReferences(includeClosedPrs: true)`、`Issue.blockedBy`、`Issue.parent`、`Issue.subIssuesSummary`、`PullRequest.closingIssuesReferences`、`PullRequest.statusCheckRollup`。`closedByPullRequestsReferences` の既定は開いているPull Requestだけで、`includeClosedPrs` でmerge済みも含む。スキーマには、ラベルが付いた時刻と、レビューとcheckの状態を読む項目もある。`LabeledEvent` に `createdAt` と `label`。`Issue.timelineItems` に `itemTypes` と `since`。`PullRequest` に `reviews`、`headRefOid`、`statusCheckRollup`。`PullRequestReview` に `author`、`state`、`commit`、`submittedAt` | GraphQLのスキーマのintrospection (2026-09-20)。公開の使い捨てのリポジトリに、roleごとの4つのGitHub Appを登録して確かめた | 実測 | 2026-09-20、2026-09-21 |
| 94 | GitHub Appが作ったPull Requestの GraphQL の `author` は `Bot` 型で、`login` に `[bot]` が付かない。RESTの `user.login` には付く (49を参照) | sandboxで実測 | 実測 | 2026-09-22、Claude Code 2.1.267 |
| 98 | GraphQLの `Repository.object(expression: "HEAD:<path>")` は、ファイルがなければ `null` を返す。`HEAD:` はそのリポジトリの既定のブランチを指す。`Blob` には `oid`、`text`、`byteSize`、`isBinary`、`isTruncated` があり、1MiBを超えるファイルは `isTruncated` になる。接続 (connection) でない項目 (`object`、`defaultBranchRef`) は、問い合わせのポイントを変えない (足す前も足したあとも `cost` は6) | GraphQLのスキーマのintrospectionと、sandboxでの実測 | 実測 | 2026-09-22 |
| 105 | 定期確認の問い合わせの1ページのポイントは「要求Issue × sub-issue × k ÷ 100」で決まる。k は接続の数で、sub-issueの下のラベルと blocked by で2、Pull Requestで1、Pull Requestの下の接続 (ラベル、check) ごとにPull Requestの件数を足す。接続の中のページサイズ (checkやラベルの件数) はポイントを変えない。公式文書の、経路に沿った `first` の積を100で割る式どおりには増えない | 公式: Rate limits and node limits for the GraphQL API。sandboxで実測 (2026-09-25) | 公式文書 + 実測 | 2026-09-25 |
| 113 | GraphQLで1つのIssueの `comments(last: 50, before: ...)` を読む問い合わせは、1ポイントである | #221 で実測 (2026-09-29) | 実測 | 2026-09-25、2026-09-29、Claude Code 2.1.273、2.1.284 |
| 127 | 定期確認の問い合わせの1ページ (sub-issueを15件、Pull Requestを2件まで。Pull Requestの下の接続は、ラベル、check、レビュー) は、14ポイントである。レビューの接続を足す前は11ポイントで、105の式のとおり3ポイント増えた。60秒の間隔で、1リポジトリが毎時840ポイントを使う | sandboxで実測 (2026-09-30) | 実測 | 2026-09-30 |
| 128 | 定期確認の問い合わせに、Pull Requestの `mergeable` と `commits(last: 1) { nodes { commit { oid committedDate } } }` を足すと、1ページは14ポイントから17ポイントになる (105の式のとおり、Pull Requestの下の接続が1つ増える)。`mergeable` はスカラーで、ポイントを変えない。`MergeableState` の値は `MERGEABLE`、`CONFLICTING`、`UNKNOWN` の3つである。`Commit.committedDate` は null にならず、`Commit.pushedDate` は「no longer supported」である。`PullRequest.headRef` は、開いているPull Requestでも `null` を返すことがあった。60秒の間隔で、1リポジトリが毎時1,020ポイントを使う | GraphQLのスキーマのintrospectionと、cumin-worksでの実測 (2026-10-03) | 実測 | 2026-10-03 |

## rulesetとcheck

| # | 制約 | 根拠 | 確度 | 日付と版 |
|---|---|---|---|---|
| 12 | Pull Requestの作成者は自分のPull Requestをapproveできない。作成者とレビュー者の身元が同じだとapproveが成立しない | docs.github.com: approving-a-pull-request-with-required-reviews | 公式文書 | 2026-09-19 |
| 13 | rulesetとbranch protectionは、Freeプラン (個人・Organizationとも) では公開リポジトリでしか使えない。非公開リポジトリで使うには個人はPro、OrganizationはTeamが要る | docs.github.com: about-rulesets、about-protected-branches | 公式文書 | 2026-09-19 |
| 15 | ラベルを条件にしたruleは、ruleの一覧に存在しない (「risk/mediumならOwnerの承認が必須」をGitHubだけでは書けない) | docs.github.com: available-rules-for-rulesets に記載なし | 公式文書 (不在の確認) | 2026-09-19 |
| 21 | ファイルのパスを制限するrule (push ruleset) は、Teamプランの非公開または内部リポジトリでしか使えない。Freeプランの公開リポジトリでは使えない | docs.github.com: about-rulesets | 公式文書 | 2026-09-19 |
| 24 | secret scanning、push protection、code scanning、dependency reviewは、公開リポジトリでは無料で使える。Dependabotは全てのプランで使える | docs.github.com: code-security/getting-started/github-security-features | 公式文書 | 2026-09-19 |
| 51 | `if` の条件で飛ばされたjobのcheck runは、`status: completed`、`conclusion: skipped` になる。必須のcheckであっても、mergeを止めない。workflow全体が飛ばされたとき (パスやブランチの絞り込みなど) は、checkが保留のまま残り、mergeを止める | 公開の使い捨てのリポジトリに、roleごとの4つのGitHub Appを登録して確かめた。workflow全体が飛ばされた場合は、docs.github.com: troubleshooting-required-status-checks | 実測 (workflow全体が飛ばされた場合は公式文書) | 2026-09-20、2026-09-21 |
| 53 | `GET /repos/{owner}/{repo}/rules/branches/{branch}` は、installation tokenで呼べて、必須のcheckの一覧が返る。要る権限は Metadata: Read-only である | docs.github.com: permissions-required-for-github-apps。公開の使い捨てのリポジトリに、roleごとの4つのGitHub Appを登録して確かめた | 公式文書 + 実測 | 2026-09-20、2026-09-21 |
| 54 | 公開リポジトリでは、installation tokenは、Checks、Commit statuses、Actions の権限がなくても、check run、commit status、check runのannotation、jobのログを読める。jobのログは、認証なしでは読めない (403)。jobのIDは、check runの `details_url` の最後の部分である。公式文書は、check runの一覧を読むには Checks: Read-only、commit statusを読むには Commit statuses: Read-only が要る、と書いている。失敗したGitHub Actionsのcheck runは、`output.title` が空で、内容はannotationに入る | docs.github.com: permissions-required-for-github-apps。公開の使い捨てのリポジトリに、roleごとの4つのGitHub Appを登録して確かめた | 実測 (権限が要るという記述は公式文書) | 2026-09-20、2026-09-21 |
| 62 | rulesetのbypass listにGitHub Appを入れられる。必須status checkは「このAppが出したものだけ有効」と発行元を固定できる。"Restrict updates" のruleがあるブランチへのPull Requestは、mergeの状態が常に `blocked` になる (`mergeable_state`、GraphQLでは `BLOCKED`)。bypass listにいる相手から見ても、必須のcheckが全て通っていても、同じである。それでも、bypass listにいる相手のmergeの呼び出しは成功する (200)。bypass listにいないAppは、405 (`Repository rule violations found`) を受け取る。`gh pr merge` には `--admin` が要る | docs.github.com: creating-rulesets-for-a-repository、available-rules-for-rulesets。公開の使い捨てのリポジトリに、roleごとの4つのGitHub Appを登録して確かめた | 公式文書 + 実測 | 2026-09-20、2026-09-21 |
| 68 | rulesetのbypassの相手の種類 `RepositoryRole` で、`actor_id: 5` は `admin` のroleである。同じ内容でrulesetを `PUT` しても、履歴の版は増えない。OAuthのtoken (`gh`) でworkflowのファイルをpushするには、`workflow` のscopeが要る。既定のブランチのworkflowを変えたあと、開いているPull Requestを閉じて開き直しても、古いworkflowが動く。"Update branch" か新しいコミットで、新しいworkflowが動く | 公開の使い捨てのリポジトリに、roleごとの4つのGitHub Appを登録して確かめた | 実測 | 2026-09-20、2026-09-21 |
| 79 | 公開リポジトリでは、標準のGitHub-hosted runner (macOSを含む) の利用は無料である。Freeプランの同時実行は、全体で20 job、macOSは5 jobまで。`macos-latest` はarm64である | 公式: About billing for GitHub Actions、Usage limits for GitHub Actions、GitHub-hosted runners reference | 公式文書 | 2026-09-21 |
| 80 | `POST /repos/{owner}/{repo}/rulesets` に、そのリポジトリにある ruleset と同じ名前を渡すと、422 "Name must be unique" が返る。同じリポジトリに、同じ名前の ruleset は2つ作れない | sandboxで実測 | 実測 | 2026-09-21 |
| 81 | `GET /repos/{owner}/{repo}/rulesets` は、`includes_parents` (初期値 `true`) により、Organizationの ruleset のうちそのリポジトリに当たるものも返す | 公式: Get all repository rulesets | 公式文書 | 2026-09-21 |
| 82 | Organizationの階層の ruleset は、GitHub Enterprise プランでだけ作れる。Free と Team の Organization では作れないので、Organizationの ruleset とリポジトリの ruleset の名前が重なることは、これらのプランでは起きない。Organizationの ruleset の名前が一意かどうかは、「Not confirmed」の節の82を参照 | 公式: About rulesets ("For organizations on the GitHub Enterprise plan, you can set up rulesets at the organization level") | 公式文書 | 2026-09-21 |
| 109 | 必須のcheckは `GET /repos/{owner}/{repo}/rules/branches/{branch}` で読み、この応答はページに分かれる。`statusCheckRollup` は、`CheckRun` なら `name`、`status`、`conclusion`、`checkSuite.app.databaseId` を、`StatusContext` なら `context`、`state` を返す | #187 で実測 (2026-09-25) | 実測 | 2026-09-25、2026-09-29、Claude Code 2.1.284 |
| 117 | rulesetで作成を止めたブランチを、止められていないAppがGit Database API (`POST /repos/{owner}/{repo}/git/refs`) で作ろうとすると、422 ("Reference update failed") が返る | sandboxのlive check (`TestLiveDiagramsBranch`、#212) で実測 (2026-09-30) | 実測 | 2026-09-29、2026-09-30 |

## mergeと閉じるリンク

| # | 制約 | 根拠 | 確度 | 日付と版 |
|---|---|---|---|---|
| 60 | Appが作った、本文に `Closes #N` のあるPull Requestを、別のAppがmergeすると、Pull Requestに閉じるリンクが付いていれば、sub-issueである #N が数秒で閉じる。2026-09-30の途中からは、作った直後に閉じるリンクが付かないことがある。手で張ったリンクでは閉じなかった日もある。どちらも今も成り立つかは分からない (「Not confirmed」の節の120と121を参照) | 公開の使い捨てのリポジトリに、roleごとの4つのGitHub Appを登録して確かめた | 実測 | 2026-09-20、2026-09-21 |
| 118 | cumin-coreのinstallation tokenで、GraphQLの `addCloseIssueReferences` を呼べる。張ったリンクは、すぐに実装Issueの `closedByPullRequestsReferences` に現れ、Issueにcumin-coreの `connected` のイベントが付く。公式のGraphQLのリファレンスは、このmutationに要る権限を書いていない | sandboxの `TestLiveCloseReferences` で実測 (#278、C1とC2、2026-09-30) | 実測 | 2026-09-30、2026-10-01 |
| 119 | cumin-coreのtokenで、Issueを `state: closed`、`state_reason: completed` で閉じられる。閉じたIssueをもう一度閉じても200が返り、`closed` のイベントは1つのままである | 同上 (C3とC4) | 実測 | 2026-09-30、2026-10-01 |
| 123 | Pull Requestのmergeで、`sha` に先頭でないコミットを渡すと、409 ("Head branch was modified") が返り、何もmergeされない | 公式: Merge a pull request。#293 の M2 | 公式文書 + 実測 | 2026-10-01 |
| 124 | 衝突するPull Requestのmergeは、405 ("Pull Request has merge conflicts") になる。rulesetに止められたmergeも405である (62)。mergeの直前に読んだ `mergeable` は、mainが動いた直後だと古い `true` のことがある | #293 の M4 | 実測 | 2026-10-01 |
| 125 | mergeが405で失敗した直後に読み直すと、衝突のときだけ `mergeable` が `false`、`mergeable_state` が `dirty` になる。衝突とrulesetの拒否は、これで見分けられる | 公式: Get a pull request。#293 の M5 | 公式文書 + 実測 | 2026-10-01 |

## git

| # | 制約 | 根拠 | 確度 | 日付と版 |
|---|---|---|---|---|
| 70 | `git worktree add <path> <branch>` は、`<branch>` がローカルになく、ちょうど1つのリモートにあれば、`git worktree add --track -b <branch> <path> <remote>/<branch>` と同じに扱われる | git-worktree: "If <commit-ish> is a branch name ... and is not found ... but there does exist a tracking branch in exactly one remote ... treat as equivalent to: git worktree add --track -b <branch> <path> <remote>/<branch>" | 公式文書 | 2026-09-20、Claude Code 2.1.267、Apple Git 2.50.1 |
| 71 | `git clone --no-checkout` は、remote-tracking branch と `remote.origin.fetch` を作る。`--bare` はどちらも作らない | git-clone: `--no-checkout` は "Do not checkout HEAD after the clone is complete"。`--bare` は "neither remote-tracking branches nor the related configuration variables are created" | 公式文書 | 2026-09-20、Claude Code 2.1.267、Apple Git 2.50.1 |
| 72 | `git fetch` は `refs/remotes/origin/HEAD` を動かさない。`git remote set-head origin --auto` がリモートに問い合わせて、`refs/remotes/origin/HEAD` をリモートの既定のブランチに向ける | git-remote: "With -a or --auto, the remote is queried to determine its HEAD, then the symbolic-ref refs/remotes/<name>/HEAD is set to the same branch"。git-fetch には HEAD の更新の記述がない | 公式文書 | 2026-09-20、Claude Code 2.1.267、Apple Git 2.50.1 |
| 73 | `git worktree list --porcelain` は、worktreeの実体のパスを出す。macOSでは、`/var` の下の一時ディレクトリが `/private/var` で出る | テストで `filepath.EvalSymlinks` と比べた | 実測 | 2026-09-20、Claude Code 2.1.267、Apple Git 2.50.1 |
| 83 | `GIT_CONFIG_GLOBAL=/dev/null` と `GIT_CONFIG_NOSYSTEM=1` を付けても、`GIT_AUTHOR_*` と `GIT_COMMITTER_*` がなければ、gitはユーザ名とホスト名から推測した作者でコミットに成功する | #74 で実測 | 実測 | 2026-09-21、2026-09-22、Claude Code 2.1.267、git 2.50.1、gh 2.101.0 |
| 96 | contextを取り消した `git clone` は、"signal: killed" で失敗する | `TestRun_CreatesTheLabelsOnceAndPollsAtTheInterval` で観測 | 実測 | 2026-09-22、Claude Code 2.1.267 |
| 115 | 親のない (orphan) ブランチ `cumin/diagrams` に置いたSVGを、`https://raw.githubusercontent.com/<owner>/<repo>/<commit>/<path>` の画像としてIssueとPull Requestに貼ると、Webでも Android のGitHubアプリでも表示される。本文のURLは書き換えられず (Camoを通らない)、`image/svg+xml` で200を返す。Mermaidの図は、Android のアプリでは "Loading" のまま表示されない | sandboxで実測 (2026-09-29、#198 の decision request) | 実測 | 2026-09-29、2026-09-30 |
| 116 | PlantUMLは、SVGに埋め込むソースのコメント (`<!--SRC=[...]-->`) の中で、`--` を `- -` と書く。XMLのコメントは `--` を含めないためである。ソースを読み戻すときは、空白を除いてから読む | #231 で観測 (2026-09-30) | 実測 | 2026-09-29、2026-09-30 |

## macOS

| # | 制約 | 根拠 | 確度 | 日付と版 |
|---|---|---|---|---|
| 63 | macOSの `security find-generic-password -s <service> -a <account> -w` は、項目のパスワードだけを出力する。1行の値は、そのまま返す。改行を含む値は、16進で返す | `man security`。Hostで確かめた | 公式文書 + 実測 | 2026-09-20、2026-09-21 |
| 64 | `security -i` は、コマンドを標準入力から読む。秘密の値を、引数に出さずに渡せる。失敗したコマンドの終了コードを返す。項目がないときの終了コードは44である。保存に失敗しても、値を出力しない | Hostで確かめた | 実測 | 2026-09-20、2026-09-21 |
| 65 | `security add-generic-password` に、存在しないkeychainのファイルのパスを渡すと、成功を報告して、既定のkeychainに書く | Hostで確かめた | 実測 | 2026-09-20、2026-09-21 |
| 67 | launchdが起動したプロセス (ログイン中のユーザのLaunchAgent) は、`security` が作ったKeychainの項目を、確認のダイアログなしで `security` から読める | Hostで確かめた | 実測 | 2026-09-20、2026-09-21 |
| 100 | `man launchd.plist`: `KeepAlive` の `SuccessfulExit` は、終了コードが0かどうかの逆の条件で起動し直す意味で、`KeepAlive` は `RunAtLoad` を含意する。`ProcessType` の `Standard` は書かないのと同じで、`Background` はCPUとI/Oを絞る。`ExitTimeOut` はSIGTERMからSIGKILLまでの時間で、初期値はシステムが決める。launchdはjobのプロセスグループに残ったプロセスを止めるが、CLIは自分のプロセスグループで動くので届かない | `man launchd.plist` | 公式文書 | 2026-09-22 |
| 101 | ログイン中のユーザの LaunchAgent は `gui/<uid>` の domain にある。Appleが求めるのは一意な `Label` だけで、逆ドメインの形は例の慣習である。launchdから起動したcuminは、Keychainの項目を確認の画面なしで読み、`kill -9` のあと11秒で起動し直され、`launchctl kill SIGTERM` で終了コード0で止まった | `man launchctl`。Hostでの実測 (#112 の記録) | 公式文書 + 実測 | 2026-09-22 |
| 103 | `security add-generic-password ... -w` は、次の引数を値として取る。`-w` のあとにkeychainのパスを書くと、パスが秘密の値として保存され、コマンドは成功を報告する。`-w` を付けない、または `-w` を最後に置いてkeychainのパスを書かないと、値を標準入力から2回読む | Hostで、一時的なkeychainと作り物の値で実測 (2026-09-22) | 実測 | 2026-09-22、2026-09-23 |
| 104 | keychainのパスを指定しない `find-generic-password` と `delete-generic-password` は、検索の一覧にある全てのkeychainを探す。`security default-keychain` は、既定のkeychainのパスを出力する | `man security` と、Hostでの実測 | 公式文書 + 実測 | 2026-09-22、2026-09-23 |

## Discord

| # | 制約 | 根拠 | 確度 | 日付と版 |
|---|---|---|---|---|
| 102 | Discordのwebhookは `POST /webhooks/{webhook.id}/{webhook.token}` で実行する。本文には `content`、`embeds`、`components`、`file`、`poll` のどれかが要り、`content` は2000文字まで。既定の応答は `204 No Content` で、メッセージの保存に失敗してもエラーにならない。`wait=true` を付けると、作られたメッセージが返る。Allowed Mentions Object の `parse` を空の配列にすると、全てのメンションが抑えられる | 公式: Execute Webhook (2026-09-22) | 公式文書 | 2026-09-22、2026-09-23 |

## 未確認 (Not confirmed)

次の行は、確かめた事実ではない。まだ試していないことと、ある日に観測しただけで、今も成り立つか分からないことである。番号は主題の表と同じ並びから付けたもので、82は確かめた部分が「rulesetとcheck」の表にある。

| # | 制約 | 根拠 | 確度 | 日付と版 |
|---|---|---|---|---|
| 6 | 枠の上限に当たったときのheadless実行の振る舞い (エラーの形、`status` の値) | 試していない | 未確認 | 2026-09-19、Claude Code 2.1.267 |
| 17 | GitHub Appのapproveが「必須承認数」に数えられるか | 公式文書に記載なし。コミュニティでは「数えられる」との報告がある | 未確認 | 2026-09-19 |
| 82 | Organizationの ruleset の名前が一意かどうか。Organizationの ruleset が、リポジトリの ruleset と同じ名前を持てるかどうか | 公式文書に記載がない。試していない。Organizationの ruleset を作れるプランは、「rulesetとcheck」の表の82を参照 | 未確認 | 2026-09-21 |
| 120 | 2026-09-30には、手で張ったリンク (画面の Development の欄、または `addCloseIssueReferences`) のPull Requestを既定のブランチにmergeしても、Issueは閉じなかった。cumin-coreのmergeで、1分待っても開いたままだった | sandboxの `TestLiveCloseReferences` で観測 (#278、C5、2026-09-30)。cumin-works #271 と #229 でも同じ | 未確認 (2026-09-30から2026-10-01に観測した。それからは測り直していない) | 2026-09-30、2026-10-01 |
| 121 | 2026-09-30の途中から、本文に `Closes #N` と書いたPull Requestに、作った直後は閉じるリンクが付かないことがある。数時間あとに付くこともある (cumin-works #268〜#270、sandbox #185 は作ってから3分はリンクがなく、2026-10-01には付いていた)。キーワードのリンクと手で張ったリンクは、`closingIssuesReferences(userLinkedOnly: true)` で見分けられる。GitHub Status に障害の表示はなく、community の discussions 209162 と 209148 に報告がある | cumin-works、sandbox、ほかの公開リポジトリで観測 (2026-09-30、2026-10-01) | 未確認 (2026-09-30から2026-10-01に観測した。それからは測り直していない) | 2026-09-30、2026-10-01 |

## 引退した番号 (Retired numbers)

次の番号の行は、同じ事実の別の行にまとめた。番号は使い回さない。前の文面は、gitの履歴にある。

| 引退した番号 | いま事実を持つ行 |
|---|---|
| 9 | 33、57。Appの名義は49 |
| 11 | 56 |
| 14 | 62 |
| 18 | 49 |
| 20 | 51 |
| 22 | 56 |
| 23 | 59 |
| 25 | 52 |
| 28 | 86 |
| 34 | 53 |
| 35 | 54 |
| 36 | 55 |
| 38 | 44 |
| 40 | 55 |
| 41 | 63 |
| 42 | 48 |
| 43 | 47 |
| 50 | 19 |
| 66 | 104 |
| 75 | 87 |
| 76 | 32 |
| 77 | 105 |
| 88 | 2 |
| 89 | 29 |
| 95 | 105 |
| 106 | 127 |
