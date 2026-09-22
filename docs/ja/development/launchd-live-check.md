# launchd での実機の確認

`cumin run` を launchd の下で動かし、Keychain、ログ、リポジトリの設定、再起動と停止を確かめる手順。[cumin本体の設計メモ](../designs/cumin-core.md) の「テストの2層」の、実機の場面に当たる。

Agent は起動しないので、利用枠は使わない。対象は sandbox のリポジトリだけにする。

## 前提

- Host の設定ファイル (`~/.config/cumin/config.toml`) に、対象が sandbox だけの `repositories` と、`work_dir` がある。
- 対象のリポジトリの持ち主の4つの App (`cumin-core` と3つの role) の Client ID が設定にあり、Keychain にそれぞれの秘密鍵がある ([セットアップの手順](setup-guide.md) の手順1)。1つでも欠けると、`cumin run` は起動せずにキーの名前を表示して終わる。
- その4つの App が、sandbox のリポジトリを選んでインストールしてある (同じ手順書の手順2)。インストールが無いと、cumin は起動はするが、token を発行できずに定期確認が毎回失敗する。
- 対象のリポジトリに `cumin/status/ready` の付いた Issue がない。あると Agent が起動して、利用枠を使う。
- 確認の間は、Owner がログインしている。launchd の LaunchAgent は、ログイン中のユーザの下でだけ動く。

## 何を確かめるか

| # | すること | 期待する結果 |
|---|---|---|
| 1 | `scripts/install.sh` と `cumin setup launchd`、`launchctl bootstrap` | job が `state = running` になる |
| 2 | ログのファイルを読む | 起動と定期確認の行が出ている。確認の画面なしで Keychain を読めている (読めなければ定期確認が失敗する) |
| 3 | リポジトリの `.cumin/config.toml` に、上書きできるキーを足して merge する | 次の定期確認で「設定を読んだ」の行が出る。その前の定期確認では出ない (blob が変わったときだけ読む) |
| 4 | Host のキー (`work_dir` など) を足して merge する | 定期確認が、ファイルの名前とキーの名前を添えたエラーで飛ばされる |
| 5 | ファイルを元に戻し、`.cumin/risk-criteria.md` を足して merge する | エラーが止まり、`risk_criteria` が `repository` になる。ファイルには文章を入れる。空白だけのファイルはエラーになり、そのリポジトリは飛ばされたままになる |
| 6 | `kill -9 <pid>` | launchd が起動し直す。`runs` が増え、ログに起動の行が増える |
| 7 | `launchctl kill SIGTERM` | `stopped` の行が出て、job が `state = not running` になる。`last exit code = 0` で、起動し直されない |
| 8 | `cumin setup launchd --remove` と、sandbox の `.cumin/` を元に戻す | job が消え、plist が消える。ログと実行ファイルは残る。Host と sandbox が元の状態に戻る |

## 動かし方

```sh
scripts/install.sh                     # ~/.local/bin/cumin に置く
~/.local/bin/cumin setup launchd       # plist を書き、launchctl のコマンドを表示する
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/dev.cloveclove.cumin.plist
launchctl print gui/$(id -u)/dev.cloveclove.cumin | grep -E "state|pid|last exit code|runs ="
tail -f ~/.local/state/cumin/cumin.log
```

`~/.local/bin` が `PATH` に入っていれば、`cumin setup launchd` と短く書ける。`scripts/install.sh` は、入っていなければ警告を出すが、実行中のシェルの `PATH` は変えない。上のようにフルパスで書けば、どちらでも動く。

リポジトリの設定を変えるときは、Pull Request にして merge する。cumin は既定のブランチからしか読まない。`.cumin/` は保護されたパスなので、Bot の Pull Request では check が失敗する。人の Pull Request では飛ばされる。

止めるときと外すとき:

```sh
launchctl kill SIGTERM gui/$(id -u)/dev.cloveclove.cumin   # 止める
~/.local/bin/cumin setup launchd --remove                   # 止めて、plist を消す
```

`--remove` が消すのは plist だけである。ログ (`~/.local/state/cumin/`) と実行ファイルは残るので、確認のあとに要らなければ手で消す。

`poll_interval` が長いと、3から5の確認に時間がかかる。確認の間だけ `"15s"` にしてもよい。設定を読むのは起動のときだけなので、`launchctl bootstrap` の前に変える。あとから変えたときは、`launchctl kickstart -k gui/$(id -u)/dev.cloveclove.cumin` で読み直させる。確認が終わったら元に戻す。

## 記録の決まり

- 結果は、対応する Issue にコメントとして残す。実行したコマンド、確かめたことと結果を書く。
- 手元の絶対パスは `~` に置き換える。設定ファイルの中身、Client ID、token、Organization の名前は書かない。
- 利用枠の数値は書かない。この確認では Agent を起動しないので、そもそも出ない。
- 確認のあとは、Host と sandbox を元の状態に戻す。Owner が LaunchAgent を残すと言ったときだけ、残す。
