# 計画

要求Issueを実装Issueに分けるまでの流れを決める。cumin が Planner を起動し、計画の結果を GitHub で確かめ、Assignee の計画結果の確認を待つまでである。受け入れ確認は [受け入れ](../accept/index.md)、Agent起動に共通のことは [Agent](../agent/index.md) で決める。

## 全体像

要求Issueが計画でたどる状態を示す。`accepting` への遷移は受け入れのカテゴリで決める。

![計画の全体像](overview.svg)

図の元ファイル: [overview.puml](overview.puml)

「Planner が動いていない」の意味は、[Agent](../agent/index.md) の `CWK-AGENT-001` で決める。

要求Issueの `cumin/status/ready` は、いつも「Planner が要求Issueを読み直す」という意味である。Maintainer は、要求Issueを書き終えたときと、Planner の質問に答えたときに付ける。Planner はすでにある sub-issue を確かめて、足りない分だけを作る。作るものがなく、sub-issue がすべて閉じていれば、受け入れ確認に進む。

Maintainer は、Planner を通さずに自分で sub-issue を書いてもよい。このときは、自分を要求Issueの Assignee にし、sub-issue にだけ `cumin/status/ready` を付ける (`CWK-PLAN-008`)。

## 要件

### 計画の Agent起動

#### `CWK-PLAN-001` 計画を始める

`cumin/status/ready` の付いた要求Issueが次のすべてを満たすとき、cumin は状態を `cumin/status/planning` に替えてから、新しいセッションで Planner を起動する。sub-issue がすでにあってもよい。

- その `cumin/status/ready` を、cumin が扱う ([権限](../access/index.md) の `CWK-ACCESS-004`)
- blocked by の Issue が、すべて閉じている (`CWK-PLAN-002`)
- 空きがある

- 検証: `test` 3つを満たす要求Issueで、Planner の起動が1回だけ起き、その前に状態が `cumin/status/planning` になっている
- 検証: `test` どれかを満たさない要求Issueは、`cumin/status/ready` のまま残る

---

#### `CWK-PLAN-002` 先の要求Issueを待つ

要求Issueが他の要求Issueに依存するときは、Maintainer が blocked by を張る。cumin は、先の要求Issueが閉じるまで、後の要求Issueの計画を始めない。先の成果がまだない main ブランチで計画しないためである。

- 検証: `test` blocked by の要求Issueが閉じたあとの定期確認で、計画が始まる

---

#### `CWK-PLAN-003` 実行の時間の上限を渡す

cumin は、Planner に渡す事実に、Implementer と Reviewer の時間の上限を入れる。Planner は、1つの実装Issueが1回の実行に収まるように分ける。

- 検証: `test` Planner に渡す事実に、Implementer と Reviewer の時間の上限がある

---

### 計画の完了判断

#### `CWK-PLAN-004` 計画の完了を判断する

cumin は、Planner が動いていないとき、要求Issueに sub-issue が1つ以上あり、そのすべてにリスクのラベルが1つだけ付いていれば、計画が完了したと判断する。Planner の `done` と `blocked` は使わない。

- 検証: `test` sub-issue がないか、リスクのラベルが0個か2個以上の sub-issue があると、Planner が `done` を返しても完了と判断しない

---

#### `CWK-PLAN-005` 計画結果の確認を求める

`cumin/status/planning` の要求Issueで、Planner が動いておらず、計画が完了し、開いている sub-issue があるとき、cumin は状態を `cumin/status/awaiting-plan-review` に替えて、通知する。

- 検証: `test` 計画が完了すると `cumin/status/awaiting-plan-review` になり、通知は1回だけである

---

#### `CWK-PLAN-006` 計画をやり直す

`cumin/status/planning` の要求Issueで、Planner が動いておらず、計画が完了しておらず、Planner の質問のコメントもないとき、cumin は状態を替えずに Planner をもう一度起動する。やり直しは、1つの `cumin/status/planning` の間に1回だけである。

- 検証: `test` 計画が完了しないと、Planner がもう一度だけ起動され、状態は `cumin/status/planning` のままである

---

#### `CWK-PLAN-007` 計画を止める

`cumin/status/planning` の要求Issueで、Planner が動いておらず、次のどちらかが成り立つとき、cumin は状態を `cumin/status/awaiting-decision` に替えて、通知する。質問のコメントがないときは、止めた理由をコメントに書く。

- `cumin/status/planning` になったあとに、Planner の質問のコメントがある
- やり直しのあとも、計画が完了していない

- 検証: `test` Planner が `blocked` を返すと、やり直さずに `cumin/status/awaiting-decision` になる
- 検証: `test` やり直しのあとも完了しないと、理由のコメントが付いて `cumin/status/awaiting-decision` になる

---

### 計画結果の確認

#### `CWK-PLAN-008` 実装を始めた印を付ける

要求Issueの Assignee が sub-issue のどれかに `cumin/status/ready` を付けたとき、cumin は要求Issueを `cumin/status/implementing` に替える。替えるのは、要求Issueが `awaiting-plan-review`、`awaiting-acceptance`、状態ラベルなしのどれかのときだけである。

- 要求Issueに状態ラベルがあるときは、要求Issueが今の状態になったあとに付いた `cumin/status/ready` だけを使う。前の計画の `cumin/status/ready` が残っていても、新しい sub-issue を確かめる前に進まないためである。
- 要求Issueに Assignee がいなければ、進めずに、通知を1回だけ送る。Planner を通さない要求Issueでは、cumin が Assignee を付ける機会 (`CWK-ACCESS-006`) がないからである。

- 検証: `test` Assignee が sub-issue に `cumin/status/ready` を付けると、要求Issueが `cumin/status/implementing` になり、Agent起動は起きない
- 検証: `test` Assignee のいない要求Issueでは状態が替わらず、通知は1回だけである
- 検証: `test` 計画のやり直しで sub-issue が増えた要求Issueは、Assignee が新しく `cumin/status/ready` を付けるまで `cumin/status/awaiting-plan-review` のままである

---

#### `CWK-PLAN-009` 残りの sub-issue の確認を求める

`cumin/status/implementing` の要求Issueで、開いている sub-issue があり、そのすべてに状態ラベルがないとき、cumin は状態を `cumin/status/awaiting-plan-review` に替えて、通知する。Assignee が一部の sub-issue にだけ `cumin/status/ready` を付けたとき、要求Issueが黙って止まらないためである。

- 検証: `test` `cumin/status/ready` を付けた sub-issue がすべて閉じ、状態ラベルのない sub-issue だけが残ると、`cumin/status/awaiting-plan-review` になり、通知は1回だけである

---

### Planner の成果物

#### `CWK-PLAN-010` 実装Issue

Planner は、要求Issueを実装Issueに分け、要求Issueの sub-issue として作る。1つの実装Issueが、1つの Pull Request になる。本文は [implementation-issue.md](../../../../templates/implementation-issue.md) の節で書く。

- 検証: `demo` 計画すると、実装Issueが sub-issue として作られ、どの本文もテンプレートの節を持つ

---

#### `CWK-PLAN-011` 実装Issueの大きさ

実装Issueは、どれも [実装Issueの大きさの基準](../../reference/issue-sizing.md) を満たし、合わせると要求Issueの範囲をすべて覆う。

- 検証: `inspection` 計画結果の確認で、Assignee が基準と範囲を確かめる

---

#### `CWK-PLAN-012` 実装の方法を決めてから分ける

Planner は、コードと外部の文書を調べ、どの実装Issueもそのまま実装できることを確かめてから分ける。調べるだけで決められないときは、Agent作業ディレクトリで小さく試してよい。試しのコードは残さず、結果を計画の全体像のコメントに書く。あとで決まる設計を条件にした実装Issueは作らない。設計文書を書く実装Issueも、その中身は計画で決める。決められないときは、`CWK-PLAN-021.3` で止まる。

- 検証: `demo` 設計の判断が要る要求Issueで計画すると、どの実装Issueにも、設計の結果を条件にした範囲や完了の条件がない
- 検証: `demo` 外部の API を試さないと決められない要求Issueで計画すると、全体像のコメントに試した結果があり、リポジトリには図のほかに何も足されていない

---

#### `CWK-PLAN-013` 依存関係

Planner は、実装Issueどうしの依存を blocked by で記録する。blocked by は同じ要求Issueの sub-issue の間だけに張り、循環させない。

- 検証: `demo` 順番のある変更を含む要求Issueで計画すると、sub-issue の間に blocked by が張られ、他の要求Issueの Issue には張られない

---

#### `CWK-PLAN-014` リスクを仮に付ける

Planner は、実装Issueごとにリスクのラベルを1つだけ付ける。基準は、Planner が受け取るリスクの基準である (`CWK-AGENT-006`)。迷ったら高いほうを付ける。`cumin/risk/high` に当たる変更は、それだけの最小の実装Issueに分ける。

- 検証: `demo` データベースのマイグレーションを含む要求Issueで計画すると、マイグレーションだけの実装Issueに `cumin/risk/high` が付く

---

#### `CWK-PLAN-015` マイルストーンを写す

要求Issueにマイルストーンがあれば、Planner は実装Issueに同じマイルストーンを付ける。

- 検証: `demo` マイルストーンの付いた要求Issueで計画すると、どの実装Issueにも同じマイルストーンが付く

---

#### `CWK-PLAN-016` Assignee が手で行う作業

Implementer が変えられないファイルの変更が要るときは、Planner はそれを、`cumin/type/owner-task` と `cumin/risk/high` を付けた、状態ラベルのない sub-issue にする。Context に Assignee が行う理由を書き、依存する実装Issueに blocked by を張り、計画の全体像のコメントの Please check に書く。

- 検証: `demo` 保護されたパスの変更を含む要求Issueで計画すると、その変更がこの形の sub-issue になる

---

#### `CWK-PLAN-017` 計画の全体像のコメント

Planner は、要求Issueに、計画の全体像のコメントを1つ書く。形式は [plan-summary.md](../../../../templates/plan-summary.md) に従う。

- 検証: `demo` 計画のあとで、要求Issueに全体像のコメントが1つだけある

---

#### `CWK-PLAN-018` 実装Issueの図

Planner は、どの実装Issueの「Where this fits」にも、変える箇所に色を付けた SVG の図を入れる。ラベルは英語で書く。図はブランチ `cumin/diagrams` の `issue-<要求Issueの番号>/` に新しいファイルとして足し、コミットを指定して示す。Planner がリポジトリに書くのは、この図だけである。

- 検証: `demo` 計画のあとで、どの実装Issueにも、コミットを指定した `cumin/diagrams` の SVG があり、対象の箇所が色で分かる

---

#### `CWK-PLAN-019` 二重に作らない

Planner は、すでにある sub-issue を確かめてから作り、同じ実装Issueを二重に作らない。

- 検証: `demo` 途中で止めた計画をもう一度させても、同じ実装Issueが二重に作られない

---

#### `CWK-PLAN-020` 要求Issueを変えない

Planner は、要求Issueの本文を書き換えず、書かれていないことを実装Issueに足さない。要ると考えたことは、要求Issueへのコメントで提案する。

- 検証: `demo` 計画のあとで、要求Issueの本文が変わっていない

---

### Planner が判断を求めるとき

#### `CWK-PLAN-021` 推測で計画しない

Planner は、次のどれかに当たったら、推測で計画せずに `blocked` を返す。止める理由は、cumin が要求Issueのコメントに書く (`CWK-AGENT-013`)。

##### `CWK-PLAN-021.1` 曖昧な要求

要求が曖昧で、実装Issueの完了の条件を書けないときは、止まる。理由には、何が決まっていないかを書く。

- 検証: `demo` 曖昧な要求Issueで計画すると、実装Issueは作られず、決まっていないことが理由に書いてある

---

##### `CWK-PLAN-021.2` 食い違う要求

要求の中に食い違いがあるときは、止まる。理由には、どこが食い違うかを書く。

- 検証: `demo` 食い違う2つの決まりを持つ要求Issueで計画すると、実装Issueは作られず、結果は `blocked` である

---

##### `CWK-PLAN-021.3` いくつもの設計案

意味のある設計案が複数あり、どれを選ぶかで計画が変わるときは、止まる。理由には、選択肢と得失を書く。

- 検証: `demo` 設計案が2つに分かれる要求Issueで計画すると、2つの案と得失が理由に書いてある

---

##### `CWK-PLAN-021.4` 大きすぎる要求

実装Issueの見込みが [要求Issueの大きさの基準](../../reference/requirement-sizing.md) の上限を超えるときは、止まる。理由には、要求Issueの分け方の案を書く。

- 検証: `demo` 上限を超える見込みの要求Issueで計画すると、実装Issueは作られず、分け方の案が理由に書いてある

---

## 受け入れテスト

| # | 場面 | 期待する結果 |
|---|---|---|
| 1 | Maintainer が要求Issueに `cumin/status/ready` を付け、計画のあと、Assignee が sub-issue のすべてに `cumin/status/ready` を付ける | 計画が始まり (`CWK-PLAN-001`)、確認を求める通知が1回届き (`CWK-PLAN-005`)、要求Issueが `cumin/status/implementing` になる (`CWK-PLAN-008`) |
| 2 | Planner が質問して止まり、Assignee が答えて `cumin/status/ready` を付け直す | `cumin/status/awaiting-decision` になり (`CWK-PLAN-007`)、付け直すと、すでにある sub-issue を確かめて計画をやり直す (`CWK-PLAN-001`、`CWK-PLAN-019`) |
| 3 | Assignee が sub-issue の一部にだけ `cumin/status/ready` を付け、それらがすべて閉じる | `cumin/status/awaiting-plan-review` に戻り、通知が1回届く (`CWK-PLAN-009`)。残りを閉じると、受け入れ確認に進む |
| 4 | Maintainer が自分で sub-issue を書き、自分を Assignee にして、sub-issue にだけ `cumin/status/ready` を付ける | Planner は起動されず、要求Issueが `cumin/status/implementing` になる (`CWK-PLAN-008`) |

## 未決事項

なし
