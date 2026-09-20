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

分けた理由:

- cumin は、Owner の認証情報も、リポジトリの管理者の権限 (Administration) も使わない ([cumin本体の要件](../requirements/cumin-core.md) の「受け持たないこと」)。管理者の権限が要る手順3は、管理者が自分の `gh` で実行する。
- 手順3には、Host も cumin も要らない。Organization の owner、Host を動かす人、リポジトリの管理者が別の人でも、それぞれが自分の持ち場で実行できる。
- 手順3が当てる内容は、読めるファイル (ruleset の JSON と workflow) である。管理者は、中身を確かめてから、自分の権限で当てられる。画面から手で当てることもできる。
- 手順1を cumin のコマンドにしているのは、秘密鍵をプロセスの中だけで扱うためと、権限の表を App の登録と token の絞り込みで1つにするためである。

手順1と2は、まだコマンドがない。それまでは [GitHub Appの登録手順](github-app-setup.md) の手順1〜3を使う。

## 前提

- 対象のリポジトリで ruleset を使える。Free プランでは、公開リポジトリだけで使える。
- 対象のリポジトリのCI (`.github/workflows`) は、cumin を導入する前に、Owner が用意する。Implementer の GitHub App には Workflows の権限がなく、workflow を足すことも変えることもできないためである。同じ理由で、保護されたパスの workflow は、管理者が手順3のスクリプトで入れる。
- 手順3を実行する人は、対象のリポジトリの管理者である。
- 手順3を実行するマシンに `gh` があり、ログインしてある。`gh` の token には `workflow` の scope が要る。workflow のファイルを push するためである。足りないときは、次を実行する。

```sh
gh auth refresh -h github.com -s workflow
```

- ラベルは用意しなくてよい。cumin が起動のときに、足りないラベルを作る。

## 手順3: リポジトリの準備

cumin-works のリポジトリを取得して、その中で実行する。

```sh
scripts/setup-repo.sh <owner>/<repo> --implementer-app <ImplementerのAppのslug> \
  [--core-app <cumin本体のAppのslug>] [--required-check <checkの名前>]... [--dry-run]
```

| 引数 | 内容 |
|---|---|
| `--implementer-app` | Implementer の App の slug。保護されたパスのcheckは、作成者が `<slug>[bot]` の Pull Request でだけ動く |
| `--core-app` | cumin本体の App の slug。mainを守る ruleset の bypass list に入れる。App を登録する前なら省ける。登録したあとに、付けてもう一度実行する |
| `--required-check` | 必須のcheckに足すcheckの名前。リポジトリのCIのjobの名前を渡す。何度でも書ける |
| `--dry-run` | 何も変えずに、何をするかと、当てる ruleset の JSON を表示する |

slug は、App の設定画面のアドレス (`https://github.com/apps/<slug>`) にある。

スクリプトがすること:

1. `gh` のログイン、`workflow` の scope、管理者の権限を確かめる。足りなければ、何も変えずに止まる。
2. 既定のブランチに、次の2つのファイルを足す。
   - `.github/workflows/cumin-protected-paths.yml`: 保護されたパスのcheck。
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
- workflow のファイルが違う内容で既にあれば、違いを表示して、上書きしない。Pull Request で直す。
- ruleset は名前で探す。あればファイルの内容に合わせ、なければ作る。

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
| `DIFFERENT ... (not overwritten)` | 表示された違いを見て、Pull Request で workflow を直す |
| `cannot read the App ...` | slug を確かめる。非公開の App は、その Organization のメンバーの `gh` でないと読めないことがある |
