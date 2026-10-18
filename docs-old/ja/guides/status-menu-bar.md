# メニューバーのアプリを作って起動する

cuminの状態をmacOSのメニューバーに表示するアプリの、ビルド、起動、停止の手順。アプリの作りと、モニターファイルの中身は [モニターファイルとメニューバーのアプリの設計](../designs/status-menu-bar.md) にある。

アプリは、`cumin run` が書くモニターファイル (`~/.local/state/cumin/monitor.json`) を読むだけである。cuminの設定も、GitHubも読まず、ネットワークを使わない。アプリがなくても、cuminは同じように動く。

## 必要なもの

- macOS 13 以降。
- Swift 5.9 以降のtoolchain。Xcode、またはCommand Line Tools (`xcode-select --install`) に入っている。`swift --version` で確かめる。
- このリポジトリのcheckout。アプリは `tools/status-bar/` にあり、Goのモジュールとは別である。third-partyの依存はない。

## ビルドする

リポジトリの先頭で実行する。

```sh
cd tools/status-bar
swift build -c release
```

実行ファイルは `tools/status-bar/.build/release/CuminStatusBar` にできる。`.build/` はgitが無視する。

## 起動する

`tools/status-bar/` で実行する。

```sh
.build/release/CuminStatusBar &
```

メニューバーに項目が1つ出る。Dockには出ない。cuminが動いていないときも起動でき、「cumin stopped」と表示する (下の「メニューの見方」)。

## ログインのときに起動する

LaunchAgentを登録すると、ログインのたびにアプリが起動する。cuminのLaunchAgent (`dev.cloveclove.cumin`) とは別のものである。

plistのテンプレートは `tools/status-bar/dev.cloveclove.cumin-status-bar.plist` にある。`__REPO__` は、リポジトリのcheckoutのパスの代わりである。手で起動したアプリがあれば、先にメニューの「Quit」で止める。リポジトリの先頭で実行する。

```sh
sed "s|__REPO__|$PWD|" tools/status-bar/dev.cloveclove.cumin-status-bar.plist \
  > ~/Library/LaunchAgents/dev.cloveclove.cumin-status-bar.plist
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/dev.cloveclove.cumin-status-bar.plist
```

- 登録すると、すぐに起動する。以後はログインで起動する。
- plistはビルドした実行ファイルのパスを指す。checkoutを別の場所に移したら、下の手順で削除してから、もう一度登録する。
- アプリを新しくするときは、ビルドし直してから `launchctl kickstart -k gui/$(id -u)/dev.cloveclove.cumin-status-bar` で起動し直す。

## 止める、削除する

| したいこと | 方法 |
|---|---|
| アプリを止める | メニューの「Quit」。LaunchAgentで起動したアプリも止まり、次のログインまで起動しない |
| LaunchAgentで、もう一度起動する | `launchctl kickstart gui/$(id -u)/dev.cloveclove.cumin-status-bar` |
| LaunchAgentを削除する | 下の2行 |

```sh
launchctl bootout gui/$(id -u)/dev.cloveclove.cumin-status-bar
rm ~/Library/LaunchAgents/dev.cloveclove.cumin-status-bar.plist
```

`bootout` は、動いているアプリも止める。

## メニューの見方

メニューバーには、区画を3つまで出す。区画は、記号1つと数字1つである。数は、Hostの全てのリポジトリの合計である。数が0の区画は出ない。

| 区画 | 記号 | 数 |
|---|---|---|
| 実行中 | 再生の三角 | 続いているAgentの実行 |
| 承認を待つ | 上げた手 | Maintainerの承認を待つIssue (計画のレビュー、mergeの判断、受け入れ) |
| 答えを待つ | 丸の中の疑問符 | Maintainerの答えを待つIssue (判断の依頼、止まったIssue) |

区画が1つもないときは、丸を1つ出す。動いているAgentも、待つIssueもない、という意味である。

メニューを開くと、次の行が並ぶ。文面は英語である。

| 行 | 意味 |
|---|---|
| `<owner>/<repo>#<番号> <題名> · <role>, <依頼の種類>` | 実行中のAgent。選ぶと、Issueをブラウザで開く |
| `<owner>/<repo>#<番号> <題名> · <種類>` | Maintainerを待つIssue。種類は `plan-review`、`merge-decision`、`acceptance`、`decision`。選ぶと、Issueを開く。`merge-decision` ではPull Requestを開く |
| `No running agent, no waiting issue` | 上の2つの行が1つもない |
| `Quota: ...` | 利用枠の状態。`open` はAgentの起動を進める。`agent starts stopped` は起動を止めている。`next try` は次に起動を試す時刻 |
| `Stop requested: ...` | cuminは、今の実行が終わったら止まる |
| `Poll error: <owner>/<repo>: ...` | そのリポジトリの最後の定期確認が失敗した |
| `Last poll: HH:mm` | 最後の定期確認の1回りが終わった時刻 (Hostの時間帯) |
| `Quit` | アプリを止める |

### 「cumin stopped」の意味

薄い丸が1つだけ出て、メニューの先頭が「cumin stopped」で始まるときは、アプリが今の状態を表示できない。古い数字を今の状態のように見せないために、区画を出さない。

| 行 | 意味 | すること |
|---|---|---|
| `cumin stopped: the monitor file is old` | 最後の定期確認から、上限 (初期値は180秒) を超えた。`cumin run` が止まっているか、定期確認の1回りが終わっていない | cuminが動いているか確かめる ([セットアップの手順](../development/setup-guide.md) の `launchctl print`)。`poll_interval` を長くしたHostでは、下の `stale_after_sec` も長くする |
| `cumin stopped: no monitor file` | モニターファイルがない。`cumin run` が、このHostでまだ定期確認を終えていない | cuminを起動する |
| `cumin stopped: the monitor file is unreadable` | ファイルを読めない、または形が違う | cuminを新しくして起動し直す。次の定期確認が上書きする |
| `The monitor file has version <n>. Update this app.` | cuminが、アプリの知らない新しい形式で書いている | アプリをビルドし直して起動し直す |

アプリは、ファイルの時刻だけで判定する。cuminのプロセスは見ない。cuminを起動し直すと、最初の定期確認の終わりに表示が戻る。

### 新しい項目の知らせ

- 承認を待つ項目、または答えを待つ項目が新しく現れると、音を1回鳴らし、その区画が点滅する。メニューを開くと、点滅は止まる。
- アプリの起動のあと、最初に読んだ項目では鳴らない。実行中の区画は、鳴らず、点滅しない。
- Discordの通知は、今までどおり `cumin run` が出す。

## 設定

`~/.config/cumin/status-bar.json` に置く。ファイルはなくてもよい。全てのキーは任意で、ないキーは初期値になる。アプリは5秒ごとに読み直すので、起動し直さなくてよい。

```json
{
  "stale_after_sec": 180,
  "sound_approval": "Glass",
  "sound_answer": "Tink",
  "blink": "new"
}
```

| キー | 意味 | 初期値 |
|---|---|---|
| `stale_after_sec` | モニターファイルを古いとする上限 (秒)。cuminの `poll_interval` の3回分を目安にする | `180` |
| `sound_approval` | 承認を待つ新しい項目で鳴らす、システムの音の名前。`""` は鳴らさない | `"Glass"` |
| `sound_answer` | 答えを待つ新しい項目で鳴らす、システムの音の名前。`""` は鳴らさない | `"Tink"` |
| `blink` | 待つ2つの区画の点滅。`off` (点滅しない)、`new` (新しい項目が現れると点滅し、メニューを開くと止まる)、`always` (区画が出ている間、点滅し続ける) | `"new"` |

- 音の名前は、`/System/Library/Sounds` にあるファイルの、拡張子を除いた名前である。
- 型が違う値と、知らない値のキーは、初期値になる。ファイルが読めないときは、全て初期値になる。
- この設定は、cuminの設定 ([設定の一覧](../development/configuration.md)) とは別である。cuminは読まない。
