# 設定の一覧

Hostの設定ファイルと、対象のリポジトリの設定ファイルに書けるキーの一覧。設定の意味と、初期値や制限の理由は、[cumin本体の要件](../requirements/cumin-core.md) の「設定」にある。ここには、キーの名前と書き方だけを書く。

この文書のはじめの節は、Hostの設定ファイルについて書く。リポジトリの設定ファイルは、最後の節にある。

## 設定ファイルの場所

- 既定の場所は `~/.config/cumin/config.toml` である。
- `cumin run --config <path>` で、別のファイルを指定できる。
- 秘密の値 (GitHub Appの秘密鍵、Discordのwebhookのアドレス) は、このファイルに書かない。

## 読み込みの決まり

- ファイルにないキーには、初期値が使われる。
- 知らないキー、型の違う値、制限を外れた値があると、`cumin run` は起動せずに、キーの名前を示して終わる。
- TOMLの書き方の誤りと、型の違う値は、最初の1つだけを表示する。TOMLのパーサが、そこで読み込みを止めるためである。知らないキーと、制限を外れた値は、全てまとめて表示する。
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
| `request_command` | 着手 (I1) のときに実行する実行ファイル。cuminは、`<owner>/<repo>` とIssueの番号の2つの引数を付けて実行し、終わるのを待つ。Agentの起動を定期確認につなぐまでの仮の設定で、つないだら消える | 空 (何も実行せず、ログに残す) | なし |
| `roles.<role>.time_limit` | Agentの実行時間の上限 | `"50m"` | 0より大きく、`"55m"` 以下 |
| `roles.<role>.cli` | Agentを動かすCLI | `"claude-code"` | v0.1では `"claude-code"` だけ |
| `roles.<role>.cli_path` | CLIの実行ファイル。ディレクトリを含まない名前は、`PATH` から探す。受け入れテストは、偽のCLIの実行ファイルを指す | `"claude"` | 空にできない |
| `roles.<role>.model` | Agentを動かすモデル。空なら、CLIの既定のモデルを使う | 空 | なし |
| `quota.five_hour.threshold` | 5h枠のしきい値 (%)。どの時間帯にも入らない時刻に使われる | `85` | 1〜100 |
| `quota.five_hour.reset_near` | 5h枠のリセットが近いとみなす残り時間 | `"30m"` | 0以上、5時間未満 |
| `quota.weekly.threshold` | weekly枠のしきい値 (%)。どの時間帯にも入らない時刻に使われる | `85` | 1〜100 |
| `quota.<window>.bands` | 時間帯ごとのしきい値。下の「時間帯」を参照 | なし | 同じ枠の時間帯は、重ねられない |
| `github_apps.<organization>.<app>` | GitHub AppのClient ID。`cumin setup github-apps` が書き込む | なし | キーを書くなら、空にできない |

- `<role>` は、`chief-engineer`、`implementer`、`reviewer` のどれかである。
- `<app>` は、`cumin-core` と、上の3つのroleのどれかである。
- `<organization>` は、対象のリポジトリの持ち主の名前である。
- `<window>` は、`five_hour` か `weekly` である。

## 時間帯

`[[quota.<window>.bands]]` を並べると、時間帯ごとにしきい値を変えられる。Ownerが使わない時間帯のしきい値を高くする、という使い方をする。

| キー | 内容 | 制限 |
|---|---|---|
| `from` | 時間帯の始まり。この時刻を含む | `"HH:MM"` の形。`"00:00"` から `"23:59"` まで |
| `to` | 時間帯の終わり。この時刻を含まない | `from` と同じ形。`from` と同じ時刻にはできない |
| `threshold` | この時間帯のしきい値 (%) | 1〜100。必須 |

- 時刻は、Hostのローカルの時刻である。
- `to` が `from` より前なら、時間帯は日付をまたぐ。`from = "23:00"`、`to = "06:00"` は、23時から翌朝の6時までを表す。
- 同じ枠の時間帯どうしは、重ねられない。重なっていると、両方の位置 (`quota.five_hour.bands[1]` など) を示して終わる。位置は、ファイルに書いた順に0から数える。5h枠とweekly枠の時間帯は、別々に調べる。
- リセットが近いときに5h枠のしきい値を100%にする決まりは、まだ作られていない。今は、値を読み込むだけである。

このファイルで決められない設定:

- 保護されたパスは、対象のリポジトリの `.cumin/config.toml` だけで決める。下の「リポジトリの設定ファイル」を参照。
- riskの基準は、TOMLのキーではなく、Markdownのファイルで上書きする。

## 例

```toml
repositories = ["example-org/example-repo"]
work_dir = "~/cumin-work"

poll_interval = "60s"
max_issues_in_progress = 1
max_review_rounds = 3
max_check_fix_requests = 3
merge_method = "squash"
request_command = ""

[roles.implementer]
time_limit = "50m"
cli = "claude-code"
cli_path = "claude"
model = ""

[quota.five_hour]
threshold = 85
reset_near = "30m"

# 23時から翌朝の6時までは、5h枠を使い切ってよい。
[[quota.five_hour.bands]]
from = "23:00"
to = "06:00"
threshold = 100

[quota.weekly]
threshold = 85

[github_apps.example-org]
cumin-core = "<Client ID>"
chief-engineer = "<Client ID>"
implementer = "<Client ID>"
reviewer = "<Client ID>"
```

## リポジトリの設定ファイル

対象のリポジトリは、一部の設定を自分で決められる。場所は、そのリポジトリの既定のブランチの `.cumin/config.toml` である。cuminは既定のブランチからだけ読む。Pull Requestのブランチの内容は効かない。

書けるキーは、上の表で「リポジトリで上書き」ができるものと、`protected_paths` だけである。

| キー | 内容 |
|---|---|
| `max_review_rounds` | Hostの設定と同じ |
| `max_check_fix_requests` | Hostの設定と同じ |
| `merge_method` | Hostの設定と同じ |
| `roles.<role>.cli` | Hostの設定と同じ |
| `roles.<role>.model` | Hostの設定と同じ |
| `protected_paths` | Agentに変更させないパスの一覧。cuminは読まない。使うのはGitHub Actionsのcheckと、Implementerへの指示である |

読み込みの決まり:

- 書かないキーには、Hostの設定ファイルの値が残る。優先順位は、初期値、Hostの設定ファイル、リポジトリの設定ファイルの順に強くなる。
- 値の制限と、制限を外れたときの文章は、Hostの設定ファイルと同じである。
- 次のものは、そのリポジトリのエラーになる。cuminはキーの名前をログに出して、そのリポジトリの定期確認を飛ばす。他のリポジトリの定期確認は続く。Ownerが直したものをmergeすると、次の定期確認から元に戻る。
  - Hostに属するキー (`repositories`、`work_dir`、`poll_interval`、`max_issues_in_progress`、`request_command`、`quota` の表、`github_apps` の表、`roles.<role>.cli_path`、`roles.<role>.time_limit`)
  - 知らないキーと、知らないroleの名前
  - 制限を外れた値
- `roles.<role>.cli_path` と `roles.<role>.time_limit` がHostのものなのは、前者がHostのパスであり、後者がGitHub Appのtokenの寿命から決まるためである。

riskの基準は、このファイルではなく、同じディレクトリの `.cumin/risk-criteria.md` で上書きする。優先順位は設定と同じで、初期値、Hostの `risk-criteria.md`、リポジトリの `.cumin/risk-criteria.md` の順に強くなる。

例:

```toml
# レビューのラウンドを2回までにし、mergeはrebaseにする。
max_review_rounds = 2
merge_method = "rebase"

protected_paths = [
  ".cumin/",
  "CLAUDE.md",
  "AGENTS.md",
  ".claude/",
]

# 表の外のキーは、表より前に書く。表のあとに書くと、その表のキーになる。
[roles.implementer]
model = "sonnet"
```
