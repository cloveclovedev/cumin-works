# GitHub Appの登録手順

cuminとAgentがGitHub上で使う身元を、roleごとのGitHub Appとして登録する手順。誰がどのroleかは [要求仕様書](../requirements/overview.md) の「GitHub上の登場人物」にある。

この文書は、手作業の手順である。コマンドとスクリプトを使う手順は [セットアップの手順](setup-guide.md) にあり、そちらを先に試す。コマンドやスクリプトが使えないときに、この文書の手順を使う。手順4と5 (ruleset) は、`scripts/setup-repo.sh` が同じことを行う。

画面の操作とラベルは、2026-09-19時点のGitHub公式ドキュメントに基づく。末尾に出典と、まだ実機で確かめていない点をまとめた。

## 登録するApp

`cloveclovedev` では次の4つを登録する。Appの名前はGitHub全体で一意でなければならず、34文字までという制限がある。別の組織で運用するときは別の名前になるので、cuminはApp名を決め打ちにせず、roleとAppの対応を設定で持つ。

| App | 使うrole | 権限 (Repository permissions) |
|---|---|---|
| `cumin-core` | cumin本体 | Contents: Read & write、Pull requests: Read & write、Issues: Read & write |
| `cumin-chief-engineer` | Chief Engineer | Issues: Read & write、Contents: Read-only |
| `cumin-implementer` | Implementer | Contents: Read & write、Pull requests: Read & write、Issues: Read-only |
| `cumin-reviewer` | Reviewer | Pull requests: Read & write、Contents: Read-only、Issues: Read-only |

この表と同じ内容を、コードの `internal/platform/github/roles.go` に持つ。Appの登録と、installation access tokenの絞り込みは、コードの表を使う。表を変えるときは、この文書、コードの表、表を固定しているテストの3つを、同じPull Requestで変える。

権限を決めた理由:

- Pull Requestのmergeに必要な権限は Pull requests ではなく Contents: Read & write である。`cumin-core` に Contents の書き込みが要るのはこのため。
- ブランチのpushにも Contents: Read & write が要る。つまり `cumin-implementer` は権限の上ではmainにもpushできてしまう。これを防ぐのが、後述のrulesetである。
- `cumin-core` の Issues: Read & write は、状態ラベルの付け替えとコメントの投稿に使う。
- `cumin-implementer` には Workflows の権限を与えない。`.github/workflows` の変更は risk/high であり、Agentに触らせないため。
- Metadata: Read-only は、他の権限を選ぶと自動で付く。

## コマンドで登録する (手順1〜3の代わり)

`cumin setup github-apps` は、GitHub App Manifest flow で4つのAppを登録し、秘密鍵をKeychainに入れ、Client ID をHostの設定ファイルに書き、インストールのページを開く。使い方と、もう一度実行したときの動きは [セットアップの手順](setup-guide.md) にある。

下の手順1〜3は、コマンドが使えないときの手作業である。

## 手順1: Appを登録する (4回繰り返す)

1. GitHubの右上のプロフィール画像をクリックし、"Your organizations" を開く。
2. `cloveclovedev` の横の "Settings" をクリックする。
3. 左のサイドバーで "Developer settings"、続けて "GitHub Apps" をクリックする。
4. "New GitHub App" をクリックする。
5. "GitHub App name" に上の表の名前を入れる。
6. "Homepage URL" (必須) に、OrganizationかcuminのリポジトリのURLを入れる。
7. "Callback URL"、"Request user authorization (OAuth) during installation"、"Enable Device Flow"、"Setup URL" は空のまま、または未選択のままにする。cuminはユーザーのトークンを使わない。
8. Webhookの "Active" のチェックを外す。cuminは自分からGitHubを確認しに行くので、webhookを受け取らない。
9. "Permissions" の Repository permissions で、上の表の権限だけを選ぶ。それ以外は "No access" のままにする。
10. "Where can this GitHub App be installed?" で "Only on this account" を選ぶ。
11. "Create GitHub App" をクリックする。
12. 作成後の画面に表示される Client ID を控える。秘密ではないので、cuminの設定ファイルに書いてよい。

## 手順2: 秘密鍵を発行する (4回繰り返す)

1. Appの設定画面 ("Developer settings" → "GitHub Apps" → 対象Appの "Edit") を開く。
2. "Private keys" の "Generate a private key" をクリックする。PEM形式のファイルがダウンロードされる。
3. PEMファイルの中身をHostの macOS のKeychainに登録し、ダウンロードしたファイルは削除する。

秘密鍵の発行は、cuminを動かせるようになってからでよい。Appの登録 (手順1) とインストール (手順3) は、秘密鍵がなくても進められる。Keychainに登録するときの項目の名前と形式は、[cumin本体の設計メモ](../designs/cumin-core.md) の「Keychainの項目」にある。

| 項目 | 値 |
|---|---|
| service | `cumin-works` |
| account | `github-app-private-key/<AppのClient ID>` |
| 値 | PEMをbase64で1行にしたもの |

値をbase64にするのは、改行を含む値を `security find-generic-password -w` で読むと、16進の文字列で返るためである (2026-09-20に実機で確かめた)。base64で1行にした値は、そのままの形で返る。

手作業で登録するときは、次のコマンドを使う。値は標準入力で `security` に渡すので、プロセスの引数には現れない (`printf` はシェルの組み込みコマンドである)。登録したら、PEMファイルを削除する。

```sh
printf 'add-generic-password -U -s cumin-works -a "github-app-private-key/%s" -w %s\n' \
  "CLIENT_ID" "$(base64 -i PATH_TO_PEM_FILE | tr -d '\n')" | security -i
```

注意: `security add-generic-password` の最後にkeychainのファイルのパスを付ける場合、そのファイルが存在しないと、エラーにならずに既定のkeychainに書き込まれる (2026-09-20に実機で確かめた)。上のコマンドはパスを付けないので、既定のkeychain (login keychain) に入る。

cuminがKeychainを読み書きするコードは `internal/platform/keychain` にある。

秘密鍵の扱い:

- 秘密鍵はリポジトリ、設定ファイル、dotfilesに置かない。cuminは起動時にKeychainから読む。複数のHostから同じ秘密鍵を使うようになったら、Secrets Managerに移す。
- 秘密鍵に有効期限はない。漏れた疑いがあるときは、同じ画面で新しい鍵を発行してから古い鍵を "Delete" する。1つのAppに25個まで鍵を持てるので、止めずに入れ替えられる。
- 登録した鍵が正しいかは、画面に表示されるfingerprintと次のコマンドの結果を比べて確かめられる。

```sh
openssl rsa -in PATH_TO_PEM_FILE -pubout -outform DER | openssl sha256 -binary | openssl base64
```

## 手順3: AppをOrganizationにインストールする (4回繰り返す)

1. Appの設定画面で "Install App" をクリックする。
2. `cloveclovedev` の横の "Install" をクリックする。
3. "Only select repositories" を選び、cuminに任せるリポジトリ (例: `peppercheck`) だけを選ぶ。
4. "Install" をクリックする。

インストールのID (installation ID) は控えなくてよい。cuminがAppとして認証したあと、`GET /repos/{owner}/{repo}/installation` で取得できる。

## 手順4: mainをrulesetで守る (リポジトリごとに1回)

mainを更新できるのをOwnerと `cumin-core` だけにする。rulesetは、Freeプランでは公開リポジトリでだけ使える。

1. リポジトリの "Settings" タブを開く。
2. 左のサイドバーの "Code and automation" の下で "Rulesets" をクリックする。
3. "New ruleset"、続けて "New branch ruleset" をクリックする。
4. "Ruleset name" に `cumin-protect-main` を入れる。`scripts/setup-repo.sh` が使う名前と同じにしておくと、あとでスクリプトを実行しても ruleset が二重にならない。
5. "Enforcement status" を "Active" にする。
6. "Bypass list" の "Add bypass" で、`cumin-core` (GitHub App) と、Ownerが該当するrole (Organization ownerまたはRepository admin) を追加する。
7. "Target branches" の "Add a target" で "Include default branch" を選ぶ。
8. ruleとして "Restrict updates"、"Restrict deletions"、"Block force pushes" を選ぶ。
9. "Create" をクリックする。

"Restrict updates" は、bypass listにいる人だけが対象ブランチを更新できるようにするruleである。bypass listにいる人は、そのrulesetの全てのruleを回避できる。将来、必須のstatus checkやレビューのruleを足すときは、別のrulesetに分ける。

## 手順5: 必須のcheckを登録する (リポジトリごとに1回。CIがある場合)

cuminは、mainに適用されるrulesetに登録された必須のcheckが全て通ってから、レビューに進む。登録がなければ、checkを待たない。

1. 手順4と同じ画面で、もう1つ "New branch ruleset" を作る。名前は `cumin-main-required-checks` にする。
2. "Enforcement status" を "Active" にし、"Target branches" で "Include default branch" を選ぶ。
3. "Bypass list" は空のままにする。手順4のrulesetに足さないのは、bypass listにいる `cumin-core` が必須のcheckまで回避できてしまうためである。
4. ruleとして "Require status checks to pass before merging" を選び、CIのcheckを登録する。
5. "Create" をクリックする。

## 確認すること

登録が終わったら、使い捨てのリポジトリで次を確かめる。公式ドキュメントに記載がなく、実機でしか分からない点である。1〜8は [実機の確認 (live test)](live-tests.md) の `TestLiveSetupChecks` が確かめる。9は、管理者が自分の `gh` で確かめる。

結果の列は、2026-09-20に、公開の使い捨てのリポジトリで確かめたものである。

| # | 確かめること | 期待する結果 | 結果 |
|---|---|---|---|
| 1 | Implementer のAppのトークンで、mainに直接pushする | rulesetに拒否される | 拒否された (`GH013: Repository rule violations found`) |
| 2 | Implementer のAppのトークンで、必須のcheckが通ったPull Requestをmergeする | rulesetに拒否される | 拒否された (405。`Cannot update this protected ref.`) |
| 3 | cumin本体のAppのトークンで、同じPull Requestをmergeする | 成功する | 成功した (200) |
| 4 | Chief Engineer のAppのトークンでIssueを作り、ラベル、sub-issue、依存関係 (blocked by) を付ける | 成功する。Issue作成時に渡したラベルが黙って捨てられないことも見る | 成功した。ラベルはIssueに付いた。sub-issueは `parent_issue_id` で作れた。blocked by は201。ラベルそのものは、先にcumin本体のAppが作った |
| 5 | Reviewer のAppのトークンで、Implementer のAppが開いたPull RequestにAPPROVEのレビューを出す | 成功する | 成功した (200、`APPROVED`) |
| 6 | Appの表示名と、コミットの作者の表示 | `<slug>[bot]` | Pull Requestの作成者も、コミットの作者も `<slug>[bot]` だった。コミットのメールアドレスを `<botのユーザID>+<slug>[bot]@users.noreply.github.com` にすると、GitHubがbotのユーザに紐づけた |
| 7 | Implementer のAppのPull Requestで、保護されたパスを変える、変えない | 変えると `cumin-protected-paths` が失敗し、変えないと通る | そのとおりだった (`failure` と `success`) |
| 8 | Appが作ったPull Requestの作成者の種類 (`user.type`) | `Bot`。保護されたパスのjobが飛ばされずに動く | APIでは `Bot` だった。jobは飛ばされずに動いた |
| 9 | 管理者の `gh` で、非公開のAppを読む (`gh api apps/<slug>`) | 読める (`scripts/setup-repo.sh --core-app` が使う) | 読めた。認証なしでは404だった |

## 認証の流れ (cuminの実装向けの要約)

1. cuminは秘密鍵でJWTに署名する (RS256。`iss` はClient ID、`exp` は10分以内)。
2. JWTで `POST /app/installations/{installation_id}/access_tokens` を呼び、installation access tokenを受け取る。tokenは1時間で失効する。
3. 発行時に `repositories` と `permissions` を指定して、tokenをそのAgentの仕事に必要な範囲に絞る。
4. cuminはAgentを起動するたびに、そのroleのAppのtokenだけを渡す。

1〜3の実装は `internal/platform/github/appauth.go` にある。`permissions` には、上の権限の表のそのAppの行をそのまま渡し、`repositories` には対象のリポジトリ1つだけを渡す。tokenとJWTは、ログにもエラーの文章にも出さない。

## 出典

- Appの登録: https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/registering-a-github-app
- 秘密鍵: https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/managing-private-keys-for-github-apps
- 自分のAppのインストール: https://docs.github.com/en/apps/using-github-apps/installing-your-own-github-app
- rulesetの作成: https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/creating-rulesets-for-a-repository
- rulesetで使えるrule: https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/available-rules-for-rulesets
- ブランチに適用されるruleの取得: https://docs.github.com/en/rest/repos/rules
- 権限とエンドポイントの対応: https://docs.github.com/en/rest/authentication/permissions-required-for-github-apps
- JWTの生成: https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-json-web-token-jwt-for-a-github-app
- installation access tokenの発行: https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-an-installation-access-token-for-a-github-app

調査で分かった制約の一覧は [measured-constraints.md](../requirements/evidence/measured-constraints.md) にある。
