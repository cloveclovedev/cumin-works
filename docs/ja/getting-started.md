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

## 動かす

```sh
go run ./cmd/cumin --help
```

サブコマンドの一覧 (`run`、`status`、`quota allow`、`setup`) が表示される。それぞれの役割は [cumin本体の要件](requirements/cumin-core.md) の「動かし方」にある。

実行ファイルを作るときは、次のようにする。リポジトリの直下にできる `cumin` は、gitの管理から外してある。

```sh
go build -o cumin ./cmd/cumin
./cumin --help
```

## 今できること

`cumin run` は、Hostの設定ファイルを読み込んで検証する。そこから先は、まだ作られていない。設定ファイルの書き方は [設定の一覧](development/configuration.md) にある。

```sh
go run ./cmd/cumin run --config <設定ファイル>
# 設定が正しいとき:   cumin run: not built yet
# 設定に問題があるとき: 問題のあるキーの名前が表示される
```

どちらの場合も、0以外の終了コードで終わる。`--config` を省くと、`~/.config/cumin/config.toml` を読む。

ほかのサブコマンドは、まだ作られていない。実行すると、作られていないことを表示して、0以外の終了コードで終わる。

サブコマンドを作るたびに、このページを更新する。
