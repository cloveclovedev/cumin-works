# Agentの実機の確認

`internal/agent` の接続部分を、本物のClaude Codeで確かめる手順。[cumin本体の設計メモ](../designs/cumin-core.md) の「テストの2層」の、実機の場面に当たる。GitHubは使わない。

## 何を確かめるか

テスト `TestLive_AgentRun` (`internal/agent/live_test.go`) が、使い捨てのリポジトリの上で、Claude Codeを3回起動する。

| 回 | 依頼 | 確かめること |
|---|---|---|
| 1 | `README.md` を読んで、`done` を返す | 正常終了。結果が `done` で要約がある。セッションの番号がある。使用率が読めて、リセット時刻が未来である |
| 2 | 1回目のセッションを続けて、さっき読んだファイルの名前を答える | セッションの番号が1回目と同じ。要約に `README.md` がある |
| 3 | `sleep 600` を実行させ、上限を20秒にする | 異常終了 (種類は「実行時間の上限」)。CLIのプロセスグループに、プロセスが残っていない |

テスト `TestLive_ReadQuota` は、着手前の使用率の確認 (`ReadQuota`) を、本物のClaude Codeで1回だけ行う。

| 確かめること |
|---|
| 使用率が読めて、2つの枠のリセット時刻が未来で、使用率が0から1の間にある。ログに `init` のイベントと `rate_limit_info` の項目の名前と、`status` の値が出る (数値は出ない) |

## 動かし方

利用枠を使うので、Ownerが同意したときだけ動かす。

```sh
CUMIN_LIVE=1 go test -race -count=1 -run TestLive -v ./internal/agent/
# 使用率の確認だけ
CUMIN_LIVE=1 go test -race -count=1 -run TestLive_ReadQuota -v ./internal/agent/
```

- `claude` は `PATH` から探す。別の実行ファイルを使うときは、環境変数 `CUMIN_CLAUDE_PATH` にパスを書く。
- CLIの環境変数は、接続部分が決まった一覧から組み立てる (設計メモの「Agentの環境」)。テストのプロセスの環境は、`PATH` や `HOME` などの一覧にあるものしか届かない。tokenと作者は、GitHubに触れないので、仮の値である。
- `CUMIN_LIVE` がなければ、テストは飛ばされる。CIでも飛ばされる。
- `TestLive_AgentRun` の3回の実行で、1回目と2回目は数十秒、3回目は上限の20秒と猶予の10秒で終わる。`TestLive_ReadQuota` は数秒で終わる。

## 記録の決まり

- 結果は、対応するIssueにコメントとして残す。実行したコマンド、確かめたことと結果、Claude Codeのバージョンを書く。
- 使用率の数値、セッションの番号、手元の絶対パスは書かない。テストのログも、infoの行だけを出し、使用率の数値を含むdebugの行は出さない。
