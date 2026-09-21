# 実機の確認 (live test)

本物の GitHub App と、使い捨てのリポジトリ (sandbox) を使って、公式ドキュメントでは分からない GitHub の振る舞いを確かめるテスト。ふだんの `go test ./...` では動かない。

## いつ実行するか

- Owner が同意したときだけ実行する。
- ある Organization に cumin-works を導入した直後に、セットアップが正しいことを確かめるために実行する。
- GitHub の振る舞いが変わった疑いがあるときに、実行し直す。

Claude Code は起動しないので、利用枠は使わない。

## 前提

- [セットアップの手順](setup-guide.md) の手順1〜3が済んでいる。
  - 4つの GitHub App が登録され、sandbox のリポジトリにインストールされている。
  - Host の設定ファイルに Client ID があり、Keychain に秘密鍵がある。
  - sandbox に `scripts/setup-repo.sh <owner>/<repo> --core-app <slug>` を実行してある。
- sandbox は、公開のリポジトリである。App の token には Checks の権限がなく、check の結果を読めるのは公開のリポジトリだけだからである。テストは最初に、認証なしでリポジトリを読めることを確かめ、読めなければ止まる。
- sandbox の保護されたパスの一覧 (`.cumin/config.toml` の `protected_paths`) に `CLAUDE.md` があり、`live/` を守っていない。テストは `live/CLAUDE.md` (保護されている) と `live/<日時>.md` (保護されていない) を使う。ファイルがなければ、初期値の一覧が使われるので、そのままでよい。合っていなければ、テストは Pull Request を作る前に止まる。
- `TestLiveGitHubFacts` には、sandbox に次の3つが要る。リポジトリの管理者が用意する。
  - `internal/platform/github/testdata/cumin-live-fixture.yml` を `.github/workflows/cumin-live-fixture.yml` として、`internal/platform/github/testdata/pull_request_template.md` を `.github/pull_request_template.md` として、既定のブランチに置く。main は保護されているので、Pull Request で入れる。
  - `scripts/setup-repo.sh <owner>/<repo> --core-app <slug> --required-check live-skipped-for-bots` を実行して、fixture の job を必須のcheckにする。
  - 足りなければ、テストは最初に止まって、足りないものを表示する。
- sandbox は、壊れてもよいリポジトリである。テストは Issue、Pull Request、ブランチ、ラベルを作り、main に小さなファイルを1つ merge する。

## 実行のしかた

```sh
CUMIN_LIVE=1 CUMIN_LIVE_REPO=<owner>/<repo> go test -count=1 -run TestLive -v ./internal/platform/github/
```

| 環境変数 | 内容 |
|---|---|
| `CUMIN_LIVE` | `1` のときだけ実行する。それ以外では skip する |
| `CUMIN_LIVE_REPO` | sandbox のリポジトリ。`<owner>/<repo>` の形 |
| `CUMIN_CONFIG` | Host の設定ファイル。省くと `~/.config/cumin/config.toml` |
| `CUMIN_LIVE_MENTION` | 任意。GitHub の login。指定すると、`TestLiveGitHubFacts` が、その人を@メンションするコメントを1つ投稿する。通知が届いたかは、その人が目で確かめる |

テストの最後に、結果の表 (番号、確かめたこと、期待、実際の結果) が Markdown で出力される。

## token の扱い

- テストは、Host の設定の Client ID と Keychain の秘密鍵から、App ごとに installation access token を発行する。token は、sandbox のリポジトリ1つと、その App の権限に絞られる。
- token は、テストのプロセスの中だけで使う。コマンドの引数、リモートのアドレス、ファイル、テストの出力には現れない。
- git には、環境変数 (`GIT_CONFIG_*`) で認証のヘッダを渡す。ユーザの git の設定は読まない。Owner の認証情報は使わない。

## 後片付け

fixture の workflow は、sandbox の全ての Pull Request で動く。`live-fail-on-marker` は `live/fail-marker` を変える Pull Request で失敗し、`live-skipped-for-bots` は bot (GitHub App) の Pull Request で飛ばされ、`live-commit-status` は commit status を1つ作る。

テストは、Pull Request、ブランチ、Issue を作った直後に、後片付けを登録する。テストが途中で失敗しても、作ったものは閉じられ、消される。main に merge した小さなファイル (`live/<日時>.md`) は残る。

## 確かめる内容

| テスト | 内容 |
|---|---|
| `TestLiveSetupChecks` | [GitHub Appの登録手順](github-app-setup.md) の「確認すること」。main への push と merge の拒否、cumin-core による merge、Issue と sub-issue と blocked by、approve、表示名、保護されたパスのcheck、作成者の種類 |
| `TestLiveGitHubFacts` | cumin の実装が前提にする GitHub の事実。check run と commit status の読み取り、token の絞り込み、飛ばされた必須のcheckと merge、失敗したcheckについて読める範囲、レビューの `state` と `commit_id`、`Closes #N` で sub-issue が閉じること、GraphQL の項目、`rules/branches`、Pull Request のテンプレート、@メンション、Pull Request へのラベル |
