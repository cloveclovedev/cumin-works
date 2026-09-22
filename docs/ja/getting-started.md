# はじめに

リポジトリを取得してから、ビルドとテストを通し、`cumin` コマンドを動かすまでの手順。

## 必要なもの

- Git
- Go。必要なバージョンは、リポジトリの直下にある `go.mod` の `go` の行に書いてある

## 取得する

```sh
git clone https://github.com/cloveclovedev/cumin-works.git
cd cumin-works
```

## ビルドとテスト

次の4つが通ることを確かめる。`gofmt -l .` は、何も表示しなければ成功である。

```sh
go build ./...
go vet ./...
go test -race ./...
gofmt -l .
```

テストは、ネットワークも、Claude Codeの利用枠も使わない。

CI (GitHub Actions) も、Pull Requestとmainへのpushのたびに、同じ4つを実行する。CIはさらに、保護されたパスのworkflowのテスト (`python3 scripts/setup-repo/test_protected_paths.py`) を実行し、macOSのrunnerで `internal/platform/keychain` のテストを実行する。Keychainのテストは、macOSの `security` コマンドを使うので、Linuxでは飛ばされる。

## 動かす

```sh
go run ./cmd/cumin --help
```

サブコマンドの一覧 (`run`、`status`、`quota allow`、`setup`) が表示される。`setup` には `github-apps` と `launchd` がある。それぞれの役割は [cumin本体の要件](requirements/cumin-core.md) の「動かし方」にある。

実行ファイルを作るときは、次のようにする。リポジトリの直下にできる `cumin` は、gitの管理から外してある。

```sh
go build -o cumin ./cmd/cumin
./cumin --help
```

## 今できること

`cumin run` は、常駐して定期確認を行う。今できるのは、着手 (I1) までである。設定ファイルの書き方は [設定の一覧](development/configuration.md) にある。

```sh
go run ./cmd/cumin run --config <設定ファイル>
```

起動すると、次の順に動く。

1. 設定ファイルを読み込んで検証する。問題があれば、キーの名前を表示して、0以外の終了コードで終わる。
2. 対象のリポジトリの持ち主ごとに、`github_apps.<owner>.cumin-core` の Client ID を確かめ、Keychain から秘密鍵を読む。Client ID か秘密鍵がなければ、キーの名前を表示して終わる。
3. 対象のリポジトリごとに、足りないラベル (`cumin/type/requirement`、`cumin/status/*`、`risk/*`) を作る。
4. `poll_interval` (初期値は60秒) ごとに定期確認を行う。`cumin/status/ready` の付いた実装Issueがあれば、ラベルを `cumin/status/implementing` に替えてから、`request_command` を実行する。`request_command` が空なら、依頼をログに残すだけである。

ログは、JSONで標準出力に出る。1回の定期確認ごとに1行 (リポジトリ、GraphQLの `cost` と `remaining`)、着手と依頼のたびに1行が出る。token や鍵は出ない。

止めるには、Ctrl-C (SIGINT) か SIGTERM を送る。動いている定期確認が終わってから、終了コード0で終わる。

`--config` を省くと、`~/.config/cumin/config.toml` を読む。cumin を止めたときに `cumin/status/implementing` のまま残ったIssueは、自動では回収されない。Ownerが `cumin/status/ready` を付け直すと、次の定期確認で着手し直す ([Issueのラベルと状態遷移](requirements/workflow/issue-states.md) の「v0.1では実装しないこと」)。

常駐させるときは、ターミナルではなく launchd から起動する。`cumin setup launchd` が、今のユーザの LaunchAgent を書き出し、`launchctl` のコマンドを表示する。手順は [セットアップの手順](development/setup-guide.md) の手順4にある。launchd から動かすと、ログは標準出力ではなく `~/.local/state/cumin/cumin.log` に出る。

ほかのサブコマンド (`status`、`quota allow`) は、まだ作られていない。実行すると、作られていないことを表示して、0以外の終了コードで終わる。

サブコマンドを作るたびに、このページを更新する。
