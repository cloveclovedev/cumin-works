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

サブコマンドは、まだどれも作られていない。実行すると、作られていないことを表示して、0以外の終了コードで終わる。

```sh
go run ./cmd/cumin run
# cumin run: not built yet
```

サブコマンドを作るたびに、このページを更新する。
