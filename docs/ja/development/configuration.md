# 設定の一覧

Hostの設定ファイルに書けるキーの一覧。設定の意味と、初期値や制限の理由は、[cumin本体の要件](../requirements/cumin-core.md) の「設定」にある。ここには、キーの名前と書き方だけを書く。

## 設定ファイルの場所

- 既定の場所は `~/.config/cumin/config.toml` である。
- `cumin run --config <path>` で、別のファイルを指定できる。
- 秘密の値 (GitHub Appの秘密鍵、Discordのwebhookのアドレス) は、このファイルに書かない。

## 読み込みの決まり

- ファイルにないキーには、初期値が使われる。
- 知らないキー、型の違う値、制限を外れた値があると、`cumin run` は起動せずに、キーの名前を示して終わる。問題が複数あれば、全て表示する。
- 時間は、`"60s"` や `"50m"` のように、単位を付けた文字列で書く。単位のない数値は使えない。

## キー

| キー | 内容 | 初期値 | 制限 |
|---|---|---|---|
| `repositories` | 対象のリポジトリの一覧。`"<owner>/<repo>"` の形の文字列の配列 | なし (必須) | 1つ以上。同じリポジトリを2回書けない |
| `work_dir` | `git worktree` を置くディレクトリ。先頭の `~/` は、ホームディレクトリに置き換わる | なし (必須) | 空にできない |
| `poll_interval` | 定期確認の間隔 | `"60s"` | 0より大きい |
| `max_issues_in_progress` | リポジトリごとに同時に進めるIssueの数 | `1` | 1以上 |
| `max_review_rounds` | レビューのラウンドの上限 | `3` | 1以上 |
| `max_check_fix_requests` | checkの修正を依頼する回数の上限 | `3` | 1以上 |
| `merge_method` | cuminがPull Requestをmergeするときの方法 | `"squash"` | `"squash"`、`"merge"`、`"rebase"` のどれか |
| `roles.<role>.time_limit` | Agentの実行時間の上限 | `"50m"` | 0より大きく、`"55m"` 以下 |
| `roles.<role>.cli` | Agentを動かすCLI | `"claude-code"` | v0.1では `"claude-code"` だけ |
| `roles.<role>.model` | Agentを動かすモデル。空なら、CLIの既定のモデルを使う | 空 | なし |
| `github_apps.<organization>.<app>` | GitHub AppのClient ID。`cumin setup github-apps` が書き込む | なし | キーを書くなら、空にできない |

- `<role>` は、`chief-engineer`、`implementer`、`reviewer` のどれかである。
- `<app>` は、`cumin-core` と、上の3つのroleのどれかである。
- `<organization>` は、対象のリポジトリの持ち主の名前である。

このファイルで決められない設定:

- 保護されたパスは、対象のリポジトリの `.cumin/config.toml` だけで決める。
- riskの基準は、TOMLのキーではなく、Markdownのファイルで上書きする。
- 利用枠のしきい値は、まだ読み込まれない。

## 例

```toml
repositories = ["example-org/example-repo"]
work_dir = "~/cumin-work"

poll_interval = "60s"
max_issues_in_progress = 1
max_review_rounds = 3
max_check_fix_requests = 3
merge_method = "squash"

[roles.implementer]
time_limit = "50m"
cli = "claude-code"
model = ""

[github_apps.example-org]
cumin-core = "<Client ID>"
chief-engineer = "<Client ID>"
implementer = "<Client ID>"
reviewer = "<Client ID>"
```
