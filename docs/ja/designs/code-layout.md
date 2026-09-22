# コードの構成の設計

- 状態: Approved
- 要件: `CLAUDE.md` の「Code」の節 (層の分け方と、共有するコードの置き場所)
- 事実の出どころ: コードそのもの。ファイルを足すか、役目を変えるPull Requestで、この文書を直す。

パッケージとファイルの受け持ちを一覧にする。レビューのときに、変えられたファイルがどこまでを受け持つかを、コードを追わずに分かるようにするためである。機能ごとの設計は、[cumin本体の設計メモ](cumin-core.md) と [Agentの実行の設計](agent-run.md) にある。

## 範囲

扱うこと:

- パッケージとファイルの受け持ちと、依存の向き。

扱わないこと:

- 機能の設計。各設計メモにある。
- 設定のキー。[設定の一覧](../development/configuration.md) にある。

## 設計

### 依存の向き

- `cmd/cumin` が全てを組み立てる。`internal/workflow` は `internal/agent`、`internal/platform/github`、`internal/core/config`、`roles` を使う。`internal/agent` は `internal/platform/github` と `internal/core/config` を使う。`internal/platform/*` は `internal/core/*` を使ってよく、逆はない。`roles` は `templates` と `internal/core/config` を使う。
- 純粋なファイル (`domain.go`、`request.go`) は標準ライブラリだけを読む。HTTPのクライアント、`os/exec`、GitHubの型を持ち込まない。判定の表形式のテストが、I/Oなしで書けるようにするためである。
- GitHubの型 (RESTの本文、GraphQLの応答) は `internal/platform/github` で止める。他のパッケージには、cuminの型 (`RepositorySnapshot`、`Label`、`User` など) だけを渡す。

### パッケージとファイル

| パッケージ | ファイル | 受け持ち |
|---|---|---|
| `cmd/cumin` | `main.go` | サブコマンドの一覧と振り分け。終了コード |
| | `run.go` | `cumin run`。設定と4つのAppの鍵を読み、skillを書き、`agent.Service` と `workflow.Service` を組み立てて動かす |
| | `setup.go` | `cumin setup github-apps` の引数と起動 |
| `internal/core/config` | `config.go` | Hostの設定ファイル (TOML) の読み込み、初期値、制限、既定のパス |
| | `repository.go` | 対象のリポジトリの `.cumin/config.toml` を、Hostの設定に重ねる |
| | `githubapps.go` | `github_apps` の表の読み書き (`cumin setup` が書く) |
| | `quota.go` | 利用枠の設定 (しきい値、時間帯) の読み込み |
| `internal/platform/github` | `appauth.go` | `AppClient`。JWTの署名、installation tokenの発行、要求の共通部分 |
| | `tokensource.go` | cumin-coreのtokenの使い回し (期限の5分前まで) |
| | `roles.go` | AppごとのGitHubの権限の表 |
| | `installations.go` | Appの情報とインストールの確認 (`GET /app` など) |
| | `manifest.go` | GitHub App Manifest flowの応答 |
| | `users.go` | botのユーザの読み取り (`GET /users/{login}`) |
| | `labels.go` | ラベルの一覧、作成、Issueのラベルの付け替え |
| | `snapshot.go` | 定期確認の1回のGraphQLの問い合わせと、その結果の型 |
| | `githubtest/fake.go` | 受け入れテストの偽GitHub。テストが使うendpointだけを持つ |
| `internal/platform/keychain` | `keychain.go` | macOSの `security` コマンドで秘密の値を読み書きする |
| | `items.go` | cuminが使うKeychainの項目の名前 |
| `internal/workflow` | `domain.go` | 純粋。スナップショットの型、ラベルの名前、定期確認の判定 (I1)、実行終了の判定 (I2) |
| | `request.go` | 純粋。ブランチの名前と、Agentへの依頼文 |
| | `labels.go` | cuminが対象のリポジトリに作るラベルの一覧 |
| | `service.go` | 定期確認のループ。スナップショットを読み、判定を適用し、Implementerを起動する (I/O) |
| `internal/agent` | `domain.go` | cuminの他の部分から見える型: 依頼、結果とそのスキーマ、使用率、実行、異常終了 |
| | `service.go` | 1回の依頼の入口 `Start` (使用率、token、身元、実行) と、Hostの警告 |
| | `claudecode.go` | Claude Codeの接続部分。引数、出力の読み取り、起動の記録の確認、時間の上限 |
| | `env.go` | CLIのプロセスの環境変数と、roleのtokenと作者の渡し方 |
| | `quota.go` | 使用率を読む最小の実行 |
| | `worktree.go` | `Workspace`。cloneとworktreeの用意と片付け |
| `internal/setup` | `domain.go`、`page.go`、`service.go` | `cumin setup github-apps`。Manifest flowの手元のページと、登録の手順 |
| `roles` | `roles.go` | roleの指示 (`<role>.md` に平易な英語の決まりを連結したもの) |
| | `skills.go` | テンプレートをskillとして書き出す |
| | `riskcriteria.go` | riskの基準の文章を、リポジトリ、Host、初期値の順で決める |
| | `*.md` | roleごとの指示の本文 |
| `templates` | `embed.go`、`*.md` | GitHubに書く文章のテンプレート。`roles` が読む |
| `scripts` | `render-diagrams.sh` | `.puml` をSVGに書き出す |
| | `setup-repo.sh`、`setup-repo/` | 対象のリポジトリの準備 (ラベル、ruleset、保護されたパスのcheck) |

## まだ決めていないこと

なし

## 後回しにしたこと

なし
