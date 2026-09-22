# セットアップの手順

cumin-works を、ある Organization とそのリポジトリに導入する手順。何もない状態から始めて、cumin がそのリポジトリで動ける状態にする。

手作業で同じことをする手順は [GitHub Appの登録手順](github-app-setup.md) にある。下のコマンドやスクリプトが使えないときの代わりになる。

## 誰が、どこで、何を使うか

手順は、誰の認証で、どのマシンで動かすかで分けてある。

| 手順 | 使う認証 | 動かす場所 | 道具 |
|---|---|---|---|
| 1. GitHub App の登録と、秘密鍵の保存 | Organization の owner のブラウザと、Host の Keychain | Host | `cumin setup github-apps` |
| 2. GitHub App のインストール | Organization の owner のブラウザ | どこでも | ブラウザ |
| 3. リポジトリの準備 | リポジトリの管理者の `gh` | どのマシンでも | `scripts/setup-repo.sh` |
| 4. cumin の常駐 | Host のユーザ | Host | `cumin setup launchd` と `launchctl` |

分けた理由:

- cumin は、Owner の認証情報も、リポジトリの管理者の権限 (Administration) も使わない ([cumin本体の要件](../requirements/cumin-core.md) の「受け持たないこと」)。管理者の権限が要る手順3は、管理者が自分の `gh` で実行する。
- 手順3には、Host も cumin も要らない。Organization の owner、Host を動かす人、リポジトリの管理者が別の人でも、それぞれが自分の持ち場で実行できる。
- 手順3が当てる内容は、読めるファイル (ruleset の JSON と workflow) である。管理者は、中身を確かめてから、自分の権限で当てられる。画面から手で当てることもできる。
- 手順1を cumin のコマンドにしているのは、秘密鍵をプロセスの中だけで扱うためと、権限の表を App の登録と token の絞り込みで1つにするためである。


## 前提

- 対象のリポジトリで ruleset を使える。Free プランでは、公開リポジトリだけで使える。
- 対象のリポジトリのCI (`.github/workflows`) は、cumin を導入する前に、Owner が用意する。Implementer の GitHub App には Workflows の権限がなく、workflow を足すことも変えることもできないためである。同じ理由で、保護されたパスの workflow は、管理者が手順3のスクリプトで入れる。
- 手順3を実行する人は、対象のリポジトリの管理者である。
- 手順3を実行するマシンに `gh` があり、ログインしてある。`gh` の token には `workflow` の scope が要る。workflow のファイルを push するためである。足りないときは、次を実行する。

```sh
gh auth refresh -h github.com -s workflow
```

- ラベルは用意しなくてよい。cumin が起動のときに、足りないラベルを作る。
- 対象のリポジトリの持ち主は、Organization である。v0.1 の `cumin setup github-apps` は、個人アカウントの GitHub App を登録しない。

個人アカウントのリポジトリから始める場合:

1. 無料の Organization を作る。
2. リポジトリの Settings のいちばん下の "Danger Zone" で "Transfer" を選び、Organization に移す。Issue、Pull Request、webhook、secrets、release は一緒に移り、古いアドレスは新しい場所に転送される。古い場所に同じ名前のリポジトリを作ると、転送は消える。
3. そのあとで、下の手順1から進める。GitHub App は Organization の側に登録する。

## 手順1と2: GitHub App の登録とインストール

Host で、Organization の owner がブラウザにログインした状態で実行する。

```sh
cumin setup github-apps --org <Organizationの名前> [--name-prefix <Appの名前の前に付ける文字>] [--config <Hostの設定ファイル>]
```

コマンドがすること:

1. 4つの App (`cumin-core`、`chief-engineer`、`implementer`、`reviewer`) のうち、この Host でまだ登録していないものを、1つずつ登録する。App ごとにブラウザで手元のページが開き、ページが manifest を GitHub に送る。GitHub の画面で "Create GitHub App" を押す。
2. App を1つ登録するたびに、秘密鍵を Keychain に入れ、そのあとで Client ID を Host の設定ファイルの `[github_apps.<Organization>]` に書く。設定ファイルの他の行とコメントは変えない。設定ファイルが symbolic link なら、リンクの先のファイルを書き換え、リンクは残す。
3. 登録が済んだら、Organization にまだインストールされていない App のインストールのページを開く。アドレスも表示する。

App の名前は `<prefix>cumin-core`、`<prefix>cumin-chief-engineer`、`<prefix>cumin-implementer`、`<prefix>cumin-reviewer` になる。App の名前は GitHub 全体で一意なので、Organization ごとに `--name-prefix` を変える。34文字を超える名前は、ブラウザを開く前にエラーになる。

インストールの画面では:

1. Organization を選ぶ。
2. "Only select repositories" を選び、cumin に任せるリポジトリだけを選ぶ。
3. "Install" を押す。

インストールが済んだかどうかは、コマンドをもう一度実行すると分かる。済んでいる App は `installed` と表示され、ページは開かない。

もう一度実行したとき:

- 登録済みの App は飛ばす。登録済みとは、Host の設定に Client ID があり、Keychain にその Client ID の鍵があることである。足りない App だけを登録する。設計の変更で App が増えたときも、同じコマンドで足りる。
- 登録済みの App は、先に全て確かめる。Keychain の鍵が鍵として読めること、GitHub がその鍵をその Client ID のものとして受け付けること、その App の持ち主がこの Organization であること、App ごとに Client ID が違うこと、App の権限がその role の権限の表と同じであることである。4つの App は権限の組み合わせが全て違うので、Client ID が role の間で入れ替わっていると、ここで分かる。同じ App を2つの role に使うと、Agent が別の role の身元と権限で動いてしまう。1つでも合わなければ、何も登録せずに止まり、その App の名前を表示する。
- Organization の名前は、大文字と小文字を区別しない。設定ファイルに書いてある綴りの表を使う。大文字と小文字だけが違う表が2つあると、止まる。
- 設定に Client ID があるのに、Keychain に鍵がない App があると、コマンドは何も登録せずに止まり、その App の名前を表示する。推測では直さない。App が GitHub に残っているなら、[GitHub Appの登録手順](github-app-setup.md) の手順2で鍵を発行し直して、Keychain に入れる。残っていないなら、設定ファイルのその行を消して、もう一度実行する。

途中で止まったとき:

- GitHub が App を登録するのは、"Create GitHub App" を押した時点ではなく、コマンドが code を交換した時点である。その前にやめたなら、GitHub には何も残らない。もう一度実行すればよい。
- 交換のあと、Client ID を設定に書く前に止まると、GitHub に App があり、Keychain に鍵があり、設定に Client ID がない状態になる。もう一度実行すると、同じ名前の App を登録しようとして、GitHub に「名前が使われている」と断られる。このときは、GitHub の画面でその App を削除してから、もう一度実行する。
- 設定ファイルが、コマンドの知らない書き方 (`github_apps` の dotted key や inline table) のときは、コマンドは App を登録する前に止まる。ファイルは変えない。`[github_apps.<Organization>]` の表の形に直してから、もう一度実行する。
- 設定ファイルのディレクトリに書き込めないときも、コマンドは App を登録する前に止まる。コマンドは、登録の前に、そのディレクトリに一時ファイルを作って消すことで確かめる。ディレクトリがなければ作る。

登録した App は、個人の設定ではなく、Organization の設定にある: `https://github.com/organizations/<Organization>/settings/apps`。

コマンドがしないこと (手作業):

| したいこと | 手順 |
|---|---|
| 登録済みの App の権限を変える | Organization の設定で App の "Edit" → "Permissions & events" で権限を変えて保存する。そのあと、Organization の owner が、インストールの画面に出る新しい権限の確認を承認する。コードの権限の表 (`internal/platform/github/roles.go`) と [GitHub Appの登録手順](github-app-setup.md) の表も、同じ Pull Request で変える |
| App を削除する | Organization の設定で App の "Edit" → "Advanced" → "Delete GitHub App"。Host の設定ファイルからその App の行を消す。Keychain の鍵 (`cumin-works` / `github-app-private-key/<Client ID>`) は、Keychain Access か `security delete-generic-password` で消す |
| 1つの App だけ登録し直す、鍵を入れ替える | コマンドにはない。App を削除して設定の行を消してから、もう一度実行する。鍵だけなら、手順2で発行し直す |

## 手順3: リポジトリの準備

cumin-works のリポジトリを取得して、その中で実行する。

```sh
scripts/setup-repo.sh <owner>/<repo> [--core-app <cumin本体のAppのslug>] \
  [--required-check <checkの名前>]... [--dry-run]
```

| 引数 | 内容 |
|---|---|
| `--core-app` | cumin本体の App の slug。mainを守る ruleset の bypass list に入れる。App を登録する前なら省ける。登録したあとに、付けてもう一度実行する |
| `--required-check` | 必須のcheckに足すcheckの名前。リポジトリのCIのjobの名前を渡す。何度でも書ける |
| `--dry-run` | 何も変えずに、何をするかと、当てる ruleset の JSON を表示する |

slug は、App の設定画面のアドレス (`https://github.com/apps/<slug>`) にある。

スクリプトがすること:

1. `gh` のログイン、`workflow` の scope、管理者の権限を確かめる。足りなければ、何も変えずに止まる。
2. 既定のブランチに、次の2つのファイルを足す。
   - `.github/workflows/cumin-protected-paths.yml`: 保護されたパスのcheck。作成者が bot (GitHub App) の Pull Request で動き、作成者が人の Pull Request では飛ばされる。App の名前は条件に書かない。名前を間違えるとcheckが飛ばされ、飛ばされたcheckは通った扱いになるためである。
   - `.cumin/config.toml`: 保護されたパスの一覧 (`protected_paths`) のひな形。
3. 次の2つの ruleset を作る。どちらも既定のブランチが対象である。

| ruleset | 内容 | bypass list |
|---|---|---|
| `cumin-protect-main` | 更新の制限、削除の制限、force push の禁止 | リポジトリの管理者の role と、`--core-app` の App |
| `cumin-main-required-checks` | 必須のcheck。`cumin-protected-paths` と、`--required-check` で渡したcheck | 空 |

- 2つに分けてあるのは、bypass list にいる cumin本体の App が、必須のcheckまで回避できないようにするためである。
- `cumin-protected-paths` は、GitHub Actions が出したものだけを有効にする。他の App が同じ名前のcheckを出しても、通らない。
- 2つめの ruleset の bypass list が空なので、既定のブランチへの直接の push は、Owner でも通らない。変更は Pull Request で行う。
- merge の前にブランチが最新であること (strict) は、求めない。

Owner が Pull Request を merge するとき:

- `cumin-protect-main` の「更新の制限」があるので、GitHub は既定のブランチへの merge を、常に「rule に止められている」と表示する (APIでは `mergeable_state` が `blocked`)。bypass list にいる人と App は、それでも merge できる。
- 画面では、merge のボタンの近くにある、rule を回避して merge する選択肢を選ぶ。`gh` では `gh pr merge --admin` を使う。`--admin` なしの `gh pr merge` は、表示を見て止まる。
- 必須のcheckの ruleset には bypass がないので、回避できるのは「更新の制限」だけである。必須のcheckが通っていなければ、merge は止まる。
- 保護されたパスのcheckは、Owner の Pull Request では飛ばされる。飛ばされたcheckは、必須のcheckとして通った扱いになる (2026-09-20に実機で確かめた)。

もう一度実行したとき:

- 同じ引数なら、結果は変わらない。ファイルは足されず、ruleset は同じ内容になる。
- `.cumin/config.toml` が既にあれば、内容が違っていても上書きしない。そのリポジトリの設定だからである。
- workflow のファイルが違う内容で既にあれば、違いを表示して、上書きしない。ruleset は当てたうえで、最後にエラーで終わる。Pull Request で直してから、もう一度実行する。
- ruleset は名前で探す。あればファイルの内容に合わせ、なければ作る。

## 手順4: cumin を常駐させる (launchd)

Host で、Owner 自身のアカウントで実行する。設定ファイルと Keychain の鍵がそろってから行う。

まず、実行ファイルを作る。`go run` が作る一時的なバイナリは launchd から使えないので、コマンドはそれを拒否する。

```sh
go build -o cumin ./cmd/cumin
sudo cp cumin /usr/local/bin/cumin      # 置き場所は任意。動かさない場所にする
```

次に、LaunchAgent を書き出す。

```sh
cumin setup launchd [--config <Hostの設定ファイル>] [--dry-run] [--force]
```

コマンドがすること:

1. 実行中の cumin の絶対パス、設定ファイル、ホームディレクトリ、`PATH` を読み取る。
2. `~/.local/state/cumin/` を作る。ログはこの下に出る。
3. `~/Library/LaunchAgents/dev.cumin-works.cumin.plist` を書く。中身と各キーの理由は [cumin本体の設計メモ](../designs/cumin-core.md) の「launchd」にある。
4. 起動、停止、再起動、削除の `launchctl` のコマンドを表示する。

`--dry-run` は、plist を表示するだけで何も書かない。既に違う内容の plist があるときは、差分を表示して上書きせずに止まる。置き換えるなら `--force` を付ける。

書き出したら、起動する。

| したいこと | コマンド |
|---|---|
| 登録して起動する (以後はログインで起動する) | `launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/dev.cumin-works.cumin.plist` |
| 止める | `launchctl kill SIGTERM gui/$(id -u)/dev.cumin-works.cumin` |
| 再起動する | `launchctl kickstart -k gui/$(id -u)/dev.cumin-works.cumin` |
| 外す (ログインでも起動しなくなる) | `launchctl bootout gui/$(id -u)/dev.cumin-works.cumin` |
| 動いているか見る | `launchctl print gui/$(id -u)/dev.cumin-works.cumin` |
| ログを見る | `tail -f ~/.local/state/cumin/cumin.log` |

- 0以外の終了コードで終わったときだけ、launchd が起動し直す。`launchctl kill SIGTERM` で止めた cumin は 0 で終わるので、止めたままになる。
- Host が再起動したあとは、Owner がログインした時点で起動する。ログインしていない間は動かない。Keychain の鍵を確認の画面なしで読めるのが、ログイン中の LaunchAgent だけだからである。
- ログのファイルは入れ替わらない。大きくなったら、止めてから消す。
- plist を書き直したら、`launchctl bootout` してから `launchctl bootstrap` し直す。

## セットアップのあとの確認

セットアップが終わったら、保護が効いていることを1回確かめる。

1. Implementer の App として、保護されたパス (例: `CLAUDE.md`) を変える Pull Request を開く。
2. `cumin-protected-paths` のcheckが失敗することを見る。失敗のログに、ファイルと、当たった一覧の項目が出る。
3. Pull Request を閉じる。

checkが飛ばされたり、通ったりしたら、保護は効いていない。workflow のファイルと、必須のcheckの ruleset を確かめる。

## 保護されたパスの一覧か、workflow を変えたとき

既定のブランチで `.cumin/config.toml` の `protected_paths` か、`cumin-protected-paths.yml` を変えたら、開いている Pull Request のそれぞれで "Update branch" を選び、checkを動かし直す。

- checkは、動くときに一覧を読む。先にcheckが通っていた Pull Request は、新しい一覧では確かめ直されない。
- Pull Request を閉じて開き直すだけでは足りない。古い workflow のままcheckが動くことを、実機で確かめている。

## 勧めるリポジトリの設定 (任意)

スクリプトは、次の2つを設定しない。リポジトリの好みであり、cumin の動作には要らないためである。

- merge の方法を squash だけにする。cumin が merge するときの方法は、設定の `merge_method` で決まる ([設定の一覧](configuration.md))。リポジトリで許す方法は、この設定と合わせる。合っていないと、cumin の merge が失敗する。
- head のブランチを、merge のあとに自動で削除する。

## ラベルの見え方

cumin は、実装Issue の `cumin/status/*` と `risk/*` のラベルを、そのIssueを閉じる Pull Request にもコピーする。Pull Request の一覧で、状態と risk を見分けるためである。判定に使うのは、Issue のラベルだけである。

## うまくいかないとき

| 表示 | 対処 |
|---|---|
| `the gh login has no "workflow" scope` | 前提にある `gh auth refresh` を実行する |
| `the gh login is not an administrator of ...` | リポジトリの管理者のアカウントで `gh auth login` する |
| `cannot add ... If a ruleset blocks the push, add the file with a pull request.` | ruleset が既にあり、ファイルがない状態である。ファイルを Pull Request で足す |
| `DIFFERENT ... (not overwritten)` と、最後のエラー | workflow がひな形と違う。保護されたパスのcheckが動かないおそれがある。ruleset は当たっている。表示された違いを見て、Pull Request で workflow を直し、もう一度実行する。workflow を直す Pull Request では、その Pull Request の側の workflow が動くので、checkは通る |
| `cannot read the App ...` | slug を確かめる。非公開の App は、その Organization のメンバーの `gh` でないと読めないことがある |
| `... is a temporary build` | `go run` で実行している。`go build -o cumin ./cmd/cumin` で作ったバイナリから実行する |
| `... holds a different job` | 同じ名前の plist が、違う内容で既にある。表示された差分を見て、置き換えてよければ `--force` を付ける |
| launchd の job が動かない | `launchctl print gui/$(id -u)/dev.cumin-works.cumin` で最後の終了コードを見る。`~/.local/state/cumin/cumin.err.log` に設定の誤りが出る |
