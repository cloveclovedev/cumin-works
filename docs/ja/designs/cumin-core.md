# cumin本体の設計メモ

状態: Approved

[cumin本体の要件](../requirements/cumin-core.md) と [Issueのラベルと状態遷移](../requirements/workflow/issue-states.md) を実装するときに、複数の要求Issueが共有する設計上の決定だけを書く。何をするかは要件文書にあり、ここでは繰り返さない。v1から持ち込んだ設計はない。

事実の出どころは、[調査・実測で確定した制約](../requirements/evidence/measured-constraints.md) の行の番号 (「実測 N」と書く) か、公式ドキュメントのページの名前で示す。

## 1. Hostに置くファイル

| ファイル | 場所 | 書く人 |
|---|---|---|
| 設定 | `~/.config/cumin/config.toml`。`--config` で変えられる | Owner と `cumin setup` |
| riskの基準 (任意) | 設定ファイルと同じディレクトリの `risk-criteria.md`。決まりは cumin本体の要件にある | Owner |
| 手元の状態 | `~/.local/state/cumin/state.json` | `cumin run` だけ |
| 使い切りの許可 | `~/.local/state/cumin/quota-allowance.json` | `cumin quota allow` だけ |

- 人が編集するファイルは `~/.config`、cuminが書くファイルは `~/.local/state` に分ける。設定のキーは、この文書では決めない。
- 手元の状態に入れるのは、要件が認めたものだけである: Issueごとの Agent のセッションの番号と check の修正を依頼した回数、枠ごとの最新の使用率とリセット時刻。使い切りの許可のファイルには、許可した5h枠のリセット時刻だけを入れる。
- 形式はJSONで、先頭に `version` を持つ。書くときは、同じディレクトリの一時ファイルに書いてから rename する。途中で止まっても、壊れたファイルが残らない。
- 1つのファイルを書くプロセスは1つだけにする。`cumin quota allow` と `cumin status` は `cumin run` とは別のプロセスなので、ファイルを介してやりとりする。書く人を分ければ、ロックが要らない。`cumin run` は、定期確認のたびに許可のファイルを読む。
- ファイルを失っても、作業は失われない。セッションは新しく始まり、回数は0に戻り、使用率は次の着手の前に読み直す。使い切りの許可は、Ownerがもう一度出す。読めないファイルは、ないものとして扱い、警告をログに出す。
- データベースは使わない。持つものが少なく、失ってもよいためである。

## 2. Keychainの項目

秘密の値は、macOS の Keychain の generic password に置く。

| 値 | service | account |
|---|---|---|
| GitHub App の秘密鍵 | `cumin-works` | `github-app-private-key/<AppのClient ID>` |
| Discord の webhook のアドレス | `cumin-works` | `discord-webhook-url` |

- 秘密鍵の項目を Client ID で引くのは、設定ファイルにある値だけで項目が決まり、App の名前や Organization の名前をコードに埋め込まずに済むためである。
- 秘密鍵の値は、PEMをbase64で1行にしたものにする。改行を含む値を `security` の `-w` で読むと、そのままの形で返らないことがある、という報告があるためである (未確認。7を参照)。
- 読むときは、`/usr/bin/security find-generic-password -s <service> -a <account> -w` を `os/exec` で呼ぶ (`man security`)。cgoも、追加の依存も要らない。
- 要件のとおり、`cumin run` の起動時に読み、メモリにだけ持つ。値をログ、エラーの文章、手元の状態に入れない。
- Keychain に触れるコードは `internal/platform/keychain` に閉じ込める。

## 3. テストの2層

| 層 | 走らせ方 | 使うもの |
|---|---|---|
| 受け入れテスト | `go test ./...`。CIでも走る。ネットワークも利用枠も使わない | 偽GitHub (`httptest`) と、偽CLI (テストが用意する実行ファイル) |
| 実機の場面 | 環境変数 `CUMIN_LIVE=1` を付けたときだけ走る。Ownerが同意したときだけ行う | sandbox のリポジトリ、本物の GitHub App、本物の Claude Code |

- 受け入れテストは、各要件文書の「上位要件のテスト」の行から作り、`TestCore01_...` のように行の番号を名前に入れる。
- 本物のGitHubクライアントを、偽GitHubに向けて動かす。クライアントの要求の組み立て方の間違いも、受け入れテストで見つけるためである。偽GitHubは、テストが使うendpointだけを持つ。
- 偽CLIは、決まった `stream-json` の出力を返すだけの実行ファイルである。打ち切りの場面では、終わらないものを使う。
- 判定のロジックは、GitHubの事実のスナップショットから動作への純粋な関数にする。細かい分岐は、この関数の表形式のテストで確かめる。
- 実機の場面の記録には、使用率の数値を書かない。

## 4. GitHubクライアント

- 標準ライブラリ (`net/http`、`encoding/json`、`crypto/rsa`) だけで書く。SDKは使わない。使うendpointが少なく、依存を増やす理由がない。
- 読み取りは、定期確認の1回分を、リポジトリごとに1つのGraphQLの問い合わせで読む。Issue、sub-issue、ラベル、blocked by、Pull Request、レビュー、checkは入れ子の関係にあり、RESTだとIssueの数に比例して要求が増えるためである。1回で読めば、判定に使うスナップショットの時点も揃う。
- 書き込みは、全てRESTで行う。ラベル、コメント、merge、sub-issue、tokenの発行がこれに当たる。GitHub App に要る権限が、RESTのendpointごとに公式ドキュメントに書かれているためである (実測 10、33)。
- 例外として、必須のcheckの一覧はRESTで読む (`GET /repos/{owner}/{repo}/rules/branches/{branch}`、実測 34)。
- 上限: GraphQLは、installation token ごとに毎時5,000ポイントで、`first` と `last` は1〜100である (公式: Rate limits and query limits for the GraphQL API)。60秒ごとの問い合わせは、この上限に対して十分に小さい。1つの接続が100件を超えたら、続きを読む。
- installation token でGraphQLの項目を読めない場合は、RESTで読む (実測 37)。そのときは、理由をこの文書の Decision log に書く。
- GitHubの型 (GraphQLの応答、RESTのDTO) は `internal/platform/github` で止める。判定のロジックには、cuminの型のスナップショットだけを渡す。

## 5. 定期確認で読む内容

対象のリポジトリごとに、開いていて `cumin/type/requirement` の付いたIssueを起点にして、次を読む。項目の名前は、GraphQLのスキーマで確かめた (実測 36 と、2026-09-20 の introspection)。

| 読むもの | 項目 | 使う行 |
|---|---|---|
| 要求Issueと、そのsub-issue。番号、id、開閉、今のラベル | `Issue.subIssues`、`labels` | R1〜R6、I1 |
| 状態ラベルが付いた時刻 | `timelineItems(itemTypes: [LABELED_EVENT])` の `createdAt` と `label` | R3、レビューのラウンド |
| blocked by のIssueの開閉 | `Issue.blockedBy` | I1 |
| Issueを閉じるPull Request。番号、開閉、merge済みか、作成者、先頭のコミット | `Issue.closedByPullRequestsReferences(includeClosedPrs: true)`、`author`、`headRefOid`、`merged` | I2、I9 |
| レビュー。出した人、結果、対象のコミット、時刻 | `PullRequest.reviews` の `author`、`state`、`commit`、`submittedAt` | I5〜I8、レビューのラウンド |
| 先頭のコミットのcheckの結果 | `PullRequest.statusCheckRollup` | I3、I4 |

ラベルが付いた時刻の使い方:

- R3は、sub-issueに `cumin/status/ready` が付いた時刻が、要求Issueに `cumin/status/awaiting-owner-review` が付いた時刻よりあとかどうかで判定する。
- レビューのラウンドは、実装Issueに最後に `cumin/status/ready` が付いた時刻と、`cumin-reviewer` の最後の `APPROVE` の時刻の、新しいほうよりあとに出たレビューを数える。
- 同じラベルが何度も付くので、ラベルごとに、いちばん新しい `LabeledEvent` を使う。今付いているかどうかは、`labels` で見る。

I9で使うものは、mergeされたPull Requestについてだけ、別に読む。Pull Requestの説明、レビューのコメント、転記先の要求Issueのコメントである。要求Issueのコメントを読むのは、転記済みの目印を探して、再起動のあとも二重に転記しないためである。

## 6. 起動前の使用率の確認

- `claude -p "/usage"` は利用枠を使わないが、人間向けの文章しか返さない。機械可読の使用率は、モデルを呼ぶ実行の `rate_limit_event` にだけ出る (実測 1、29)。
- そこで、着手 (R1、I1) の直前に、最小の実行を1回行う。`--system-prompt` で短い指示に置き換え、1語だけ答えさせ、`--output-format stream-json --verbose` で `rate_limit_event` を読む (実測 30)。実行は数秒で終わり、使う利用枠は小さい。Agentの起動と同じく、`--setting-sources project` を付ける。
- `/usage` の文章を解析する方法は採らない。人間向けの形式は、予告なく変わりうるためである。
- `rate_limit_event` も公式ドキュメントにない (実測 2)。イベントがない、または形が違うときは「読み取れなかった」として扱い、着手せずにOwnerに通知する (Q1)。安全な側に倒す。
- この確認は Claude Code に固有なので、Claude Code の接続部分 (`internal/agent`) に置く。`internal/quota` が受け取るのは、枠ごとの使用率とリセット時刻だけである。

## 7. まだ確かめていないこと

| 確かめること | 確かめる要求Issue |
|---|---|
| installation token で、5の GraphQL の項目を読めるか (実測 36) | #6 |
| 改行を含む値を `security ... -w` で読んだときの形。launchd から起動したプロセスが、確認の画面なしで項目を読めるか | #5 |
| 公開リポジトリで、checkの結果を読むのに要る権限 (実測 35) | #5 |
| 枠の上限に当たったときの、headless実行の終わり方 (実測 6) | 起動前の使用率の確認を作る要求Issue |

## Decision log

- 2026-09-20: 最初の版。手元の状態ファイル、Keychainの項目、テストの2層、GitHubクライアント、定期確認で読む内容、起動前の使用率の確認を決めた (#4、#13)。
