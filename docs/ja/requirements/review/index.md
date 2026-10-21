# レビュー

必須チェックが完了した Pull Request を Reviewer がレビューし、指摘を Implementer が直すまでの流れを決める。cumin が Reviewer を起動し、レビューを GitHub で確かめ、ラウンドを数え、上限で Assignee に回すまでである。マージとマージ判断は [マージ](../merge/index.md)、Agent起動に共通のことは [Agent](../agent/index.md) で決める。

## 全体像

実装Issueがレビューでたどる状態を示す。灰色の状態は、ほかのカテゴリで決める。

![レビューの全体像](overview.svg)

図の元ファイル: [overview.puml](overview.puml)

「Reviewer が動いていない」の意味は、[Agent](../agent/index.md) の `CWK-AGENT-001` で決める。

Reviewer と Implementer は、セッションを共有しない。Reviewer が見るのは、GitHub に出ているものとリポジトリの中身だけである。

## 要件

### レビューの Agent起動

#### `CWK-REVIEW-001` レビューを始める

`cumin/status/checking` の実装Issueで、必須チェックが先頭のコミットで完了したとき (`CWK-CHECK-002`)、cumin は状態を `cumin/status/reviewing` に替えてから、Reviewer を起動する。1ラウンド目は新しいセッションで、2ラウンド目からは Reviewer の直前のセッションで起動する。

- 検証: `test` 必須チェックが完了すると `cumin/status/reviewing` になり、Reviewer の起動が1回だけ起きる
- 検証: `test` 指摘の修正のあとは、Reviewer の直前のセッションで起動する

---

#### `CWK-REVIEW-002` Reviewer に渡す事実

cumin は、Reviewer に次の事実を渡す。

- 何ラウンド目かと、ラウンドの上限
- 先頭のコミット
- 2ラウンド目からは、前のラウンドでレビューしたコミット
- Reviewer がこの Pull Request を承認したことがあれば、最後に承認したコミット

- 検証: `test` 2ラウンド目に渡す事実に、ラウンド、上限、先頭のコミット、前のラウンドのコミットがある
- 検証: `test` 承認のあとの1ラウンド目には、最後に承認したコミットがある。承認したことがなければ、ない

---

### レビューの完了判断

#### `CWK-REVIEW-003` レビューの完了を判断する

cumin は、`cumin-reviewer` が今の先頭のコミットに `APPROVE` か `REQUEST_CHANGES` のレビューを出したとき、レビューが完了したと判断する。古いコミットへのレビュー、`COMMENT` だけのレビュー、Reviewer の結果は使わない。

- 検証: `test` 古いコミットへの `APPROVE` と、今の先頭のコミットへの `COMMENT` は、レビューがないものとして扱う

---

#### `CWK-REVIEW-004` 指摘を修正する

`cumin/status/reviewing` の実装Issueで、Reviewer が動いておらず、最新のレビューが今の先頭のコミットへの `REQUEST_CHANGES` で、ラウンドが上限に達していないとき、cumin は状態を `cumin/status/implementing` に替えて、Implementer の直前のセッションで指摘の修正を頼む。

- 検証: `test` `REQUEST_CHANGES` のあとで `cumin/status/implementing` になり、Implementer の直前のセッションで起動が1回起きる

---

#### `CWK-REVIEW-005` チェックに戻る

`cumin/status/reviewing` の実装Issueで、Reviewer が動いておらず、次のどちらかが成り立つとき、cumin は状態を `cumin/status/checking` に替える。古いコミットへのレビューも、ラウンドに含めたままにする。

- レビューの間に、先頭のコミットが変わった
- `APPROVE` が出ているが、必須チェックが今の先頭のコミットで完了していない

- 検証: `test` レビューの間に push されると `cumin/status/checking` に戻り、必須チェックが完了するともう一度レビューされる

---

#### `CWK-REVIEW-006` レビューをやり直す

`cumin/status/reviewing` の実装Issueで、Reviewer が動いておらず、今の先頭のコミットに Reviewer のレビューも質問のコメントもないとき、cumin は状態を替えずに Reviewer をもう一度起動する。やり直しは、1つの `cumin/status/reviewing` の間に1回だけである。

- 検証: `test` レビューを出さずに終わると、Reviewer がもう一度だけ起動され、状態は `cumin/status/reviewing` のままである

---

#### `CWK-REVIEW-007` レビューを止める

`cumin/status/reviewing` の実装Issueで、Reviewer が動いておらず、次のどれかが成り立つとき、cumin は状態を `cumin/status/awaiting-decision` に替えて、通知する。質問のコメントがないときは、止めた理由をコメントに書く。当たる理由はすべて書く。

- `cumin/status/reviewing` になったあとに、Reviewer の質問のコメントがある。質問ではやり直さない
- やり直しのあとも、今の先頭のコミットにレビューがない
- リスクのラベルがないか、2つ以上ある
- 実装Issueを閉じる開いている Pull Request がない

- 検証: `test` Reviewer が `blocked` を返すと、やり直さずに `cumin/status/awaiting-decision` になる
- 検証: `test` リスクのラベルを外したときと、Pull Request が閉じられたときは、それぞれの理由のコメントが付いて止まる

---

### ラウンド

#### `CWK-REVIEW-008` ラウンドの数え方

ラウンドは、次の2つの新しいほうよりあとに出た `cumin-reviewer` のレビューの数とする。cumin はラウンドの数を手元に持たない。

- 実装Issueに最後に `cumin/status/ready` が付いたとき
- `cumin-reviewer` が最後に `APPROVE` を出したとき

`APPROVE` のあとで数え直すのは、衝突の解消や Assignee の `REQUEST_CHANGES` (`CWK-MERGE-004`) で先頭のコミットが変わり、レビューをやり直すからである。

- 検証: `test` `cumin/status/ready` の付け直しのあとと、`APPROVE` のあとで、ラウンドが1から数え直される

---

#### `CWK-REVIEW-009` 原因を整理する

`cumin/status/reviewing` の実装Issueで、Reviewer が動いておらず、最新のレビューが今の先頭のコミットへの `REQUEST_CHANGES` で、ラウンドが上限 (`max_review_rounds`、初期値は5) に達し、原因の整理のコメントがまだないとき、cumin は状態を替えずに、Reviewer の直前のセッションで原因の整理を頼む。

- 検証: `test` 上限のラウンドで `REQUEST_CHANGES` が出ると、指摘の修正ではなく、原因の整理の起動が1回起きる

---

#### `CWK-REVIEW-010` ラウンドの上限で止める

`cumin/status/reviewing` の実装Issueで、Reviewer の原因の整理のコメントが Pull Request にあるとき、cumin は状態を `cumin/status/awaiting-decision` に替えて、通知する。

- 検証: `test` 原因の整理のコメントが付くと `cumin/status/awaiting-decision` になり、通知は1回だけである

---

### Reviewer の成果物

#### `CWK-REVIEW-011` レビュー

Reviewer は、今の先頭のコミットに GitHub のレビューを1つ出す。修正が必要な指摘が残っておらず、提案のすべてに Implementer の返答があるときだけ `APPROVE` にし、それ以外は `REQUEST_CHANGES` にする。元からある問題 (`CWK-REVIEW-013`) は、この判断に使わない。`COMMENT` だけのレビューにはしない。指摘とまとめは [review.md](../../../../templates/review.md) の形式で書き、まとめにラウンドと上限を書く。

- 検証: `demo` 正しい Pull Request で、指摘がなければ `APPROVE` が出る
- 検証: `demo` 完了の条件を満たしていない Pull Request で `REQUEST_CHANGES` が出て、修正が必要な指摘に場所と理由がある
- 検証: `demo` 提案だけがある Pull Request で、1ラウンド目は `REQUEST_CHANGES` になり、すべての提案に返答があるラウンドで `APPROVE` になる

---

#### `CWK-REVIEW-012` 修正が必要な指摘

Reviewer は、指摘を、修正が必要なもの `(blocking)` と提案 `(non-blocking)` に分ける。修正が必要な指摘には、何が問題か、なぜ直すか、どう直すかを書く。修正が必要な指摘にしてよいのは、次のものだけである。

- 完了の条件を満たしていない
- 動作が正しくない。テストが足りず、正しさを確かめられない
- 不具合の修正が、根本の原因ではなく症状だけを抑えている
- 実装Issueの範囲を超えた変更がある
- 直すべき文書が直されていない
- リポジトリにある指示 (`CLAUDE.md`、`AGENTS.md` など) に反している
- セキュリティの問題がある (`CWK-REVIEW-016`)
- 計算の手間が、現実的なデータ量で明らかに問題になる。完了の条件の性能の目標を満たしていない場合も含む
- 読んでも正しさを確かめられないほど入り組んでいる

同じ種類の誤りがいくつもの箇所にあれば、1つの指摘にまとめ、すべての箇所を挙げる。直さなくても正しく動く改善案は、提案にする。書き方の好み、言い回し、片付けだけのことは、提案にもしない。

- 検証: `demo` 正しく動くが書き方に好みの分かれる Pull Request で、その点は指摘されず、`APPROVE` になる
- 検証: `demo` 同じ誤りが3か所にある Pull Request で、指摘は1つで、3か所が挙げてある
- 検証: `demo` 症状だけを抑える不具合の修正で、根本の原因を直すよう求める指摘が出る

---

#### `CWK-REVIEW-013` 元からある問題

Reviewer は、Pull Request が持ち込んだのではなく、前から main ブランチにある問題に気づいたら、元からある問題 `(pre-existing)` として書く。元からある問題は `APPROVE` を止めず、Implementer は直さず、返答も要らない。マージのあとに cumin がフォローアップノートに書き ([マージ](../merge/index.md))、受け入れ確認で Planner が扱いを決める ([受け入れ](../accept/index.md))。

- 検証: `demo` main ブランチの不具合の近くを変える正しい Pull Request で、その不具合が元からある問題として書かれ、`APPROVE` が出る

---

#### `CWK-REVIEW-014` ラウンドごとに見る範囲

Reviewer は、1ラウンド目でできる限り洗い出し、2ラウンド目からは指摘が直ったかと返答だけを見る。毎回一から見直すと、新しい指摘がなくならないためである。

##### `CWK-REVIEW-014.1` 1ラウンド目

Reviewer は、Agent の CLI に組み込みのコードレビューとセキュリティレビューの skill (Claude Code では `code-review` と `security-review`) を実行する。`code-review` の深さは `CWK-REVIEW-015` で決める。GitHub に投稿する、ファイルを変える、クラウドで動く使い方はしない。skill が挙げた候補は、コードを読んで確かめてから指摘にする。skill が見ないこと (完了の条件、範囲、文書、リポジトリにある指示、専門知識のファイルのセキュリティの観点) は、Reviewer が確かめる。skill がないか失敗したときは、同じ観点を自分で確かめる。

- 検証: `demo` 1ラウンド目で2つの skill が実行され、まとめに実行した skill が書いてある

---

##### `CWK-REVIEW-014.2` 承認のあとの1ラウンド目

Reviewer がこの Pull Request を承認したことがあれば、最後に承認したコミット (`CWK-REVIEW-002`) から今の先頭のコミットまでの差分だけを、1ラウンド目と同じ深さで見る。skill もその差分に対して実行する。承認したことがなければ、全体を見る。マージ判断での `REQUEST_CHANGES` や衝突の解消のたびに、全体をレビューし直さないためである。

- 検証: `demo` 承認のあとに新しいコミットが入った Pull Request で、承認したコミットからの差分だけを見て、skill もその差分に対して実行する

---

##### `CWK-REVIEW-014.3` 2ラウンド目から

Reviewer は、前の指摘と返答、前のラウンドのコミットから今の先頭のコミットまでの差分だけを見る。新しく指摘してよいのは、直し方の誤り、納得できない返答、その差分の中の動作の誤りとセキュリティの問題だけである。同じ指摘を繰り返さず、skill も実行しない。

- 検証: `demo` 2ラウンド目で、前の指摘が直り、提案のすべてに返答があることを確かめて `APPROVE` を出す
- 検証: `demo` 2ラウンド目で、差分の外の問題に気づいても、指摘を足さない

---

#### `CWK-REVIEW-015` レビューの深さ

Reviewer は、リスクのラベルに合わせて、コードレビューの skill の深さ (Claude Code では effort) を選ぶ。対応は、専門知識のファイルに書く。初期の専門知識 `software-engineering` では、`cumin/risk/low` と `cumin/risk/medium` は `high`、`cumin/risk/high` は `xhigh` である。

- 検証: `inspection` Reviewer が受け取る専門知識のファイルに、リスクのラベルごとの深さがある
- 検証: `demo` `cumin/risk/high` の実装Issueの1ラウンド目で、`code-review` が `xhigh` で実行される

---

#### `CWK-REVIEW-016` セキュリティの観点とチェックに任せること

Reviewer は、専門知識のファイルにある観点で、セキュリティの問題を確かめる。秘密の値の検出、脆弱性のある依存、静的解析、書式、命名、複雑さのしきい値のように機械で確かめられることは、必須チェックに任せ、Reviewer は見ない。

- 検証: `inspection` Reviewer が受け取る専門知識のファイルに、セキュリティの観点と、チェックに任せることがある

---

#### `CWK-REVIEW-017` リポジトリのレビューの指針

対象のリポジトリの最上位に `REVIEW.md` があれば、Reviewer はそれに従う。`REVIEW.md` には、Maintainer が、そのリポジトリで何が失敗にあたるか、見なくてよいパス、指摘の根拠の示し方などを書く。

- 検証: `demo` 「`generated/` は見ない」と書いた `REVIEW.md` のあるリポジトリで、`generated/` の中の変更に指摘が出ない

---

#### `CWK-REVIEW-018` 原因の整理のコメント

原因の整理を頼まれた Reviewer (`CWK-REVIEW-009`) は、Pull Request に Assignee 向けのコメントを1つ書く。形式は [decision-request.md](../../../../templates/decision-request.md) に従い、決めてほしいこと、何が決まっていないために指摘が直らないかとそれをどこに書くべきか、指摘ごとの Reviewer と Implementer の立場、選べる決め方と得失、勧める案を書く。

- 検証: `demo` 上限のラウンドで指摘が残ると、1行目に決めてほしいことがあり、選択肢と勧める案のあるコメントが付く

---

#### `CWK-REVIEW-019` 直さない

Reviewer は、コードを変えず、push せず、Pull Request をマージせず、閉じず、Issue の本文を書き換えない。push は権限でも止まる (`CWK-ACCESS-012`)。

- 検証: `demo` レビューのあとで、Pull Request のコミット、状態、Issue の本文が変わっていない

---

### Implementer の指摘の修正

#### `CWK-REVIEW-020` 指摘への返答

Implementer は、修正が必要な指摘と提案のすべてに返答する。元からある問題には返答しなくてよい。形式は [review-reply.md](../../../../templates/review-reply.md) に従う。

- 検証: `demo` 指摘の修正のあとで、修正が必要な指摘と提案のすべてに返答がある

---

#### `CWK-REVIEW-021` 提案の扱い

Implementer は、提案を次の3つに分け、1行の理由を付けて返答する。

- この Pull Request で直す: 範囲の中で直せるもの。直して `Fixed` と返答する
- 判断が要る: 変えずに `Deferred` と返答する
- 対応しない: 変えずに `Not changed` と返答する

提案を `Follow-up` に写さない。直されなかった提案は、マージのあとに cumin がフォローアップノートに書く ([マージ](../merge/index.md))。

- 検証: `demo` 範囲の中の小さな提案は `Fixed` になり、範囲の外の提案は `Deferred` か `Not changed` になって、`Follow-up` に写されない

---

### Reviewer が判断を求めるとき

#### `CWK-REVIEW-022` 推測でレビューしない

Reviewer は、次のどれかに当たったら、レビューを出さずに `blocked` を返す。cumin は、やり直さずに止める (`CWK-REVIEW-007`)。

##### `CWK-REVIEW-022.1` 合わないリスク

リスクのラベルが実際の変更と合わないときは、止まる。例えば `cumin/risk/low` なのに、データベースのマイグレーションを含む。認証、暗号、セッションの処理を変える変更は、リスクの基準の初期値では `cumin/risk/high` である。リスクを決め直すのは Assignee なので、Implementer には回さない。

- 検証: `demo` マイグレーションを含む `cumin/risk/low` の実装Issueでレビューを頼むと、レビューは出ず、リスクが合わないことが理由に書いてある

---

##### `CWK-REVIEW-022.2` 関係のない Pull Request

Pull Request の内容が、実装Issueとほとんど関係がないときは、止まる。

- 検証: `demo` 関係のない変更の Pull Request でレビューを頼むと、結果は `blocked` である

---

##### `CWK-REVIEW-022.3` 曖昧な完了の条件

完了の条件が曖昧で、満たしたかを判定できないときは、止まる。

- 検証: `demo` 完了の条件が曖昧な実装Issueでレビューを頼むと、判定できない条件が理由に書いてある

---

## 受け入れテスト

| # | 場面 | 期待する結果 |
|---|---|---|
| 1 | Reviewer が `REQUEST_CHANGES` を出し、Implementer が直し、2ラウンド目で `APPROVE` が出る | レビューが始まり (`CWK-REVIEW-001`)、修正を頼み (`CWK-REVIEW-004`)、必須チェックのあと、2ラウンド目が前の指摘と返答と差分だけを見て承認する (`CWK-REVIEW-014.3`) |
| 2 | 上限のラウンドでも指摘が残る | 原因の整理を頼み (`CWK-REVIEW-009`)、コメントが付くと `cumin/status/awaiting-decision` になって通知が1回届く (`CWK-REVIEW-010`、`CWK-REVIEW-018`) |
| 3 | 承認のあとに衝突が起き、Implementer が解消する | ラウンドが1から数え直され (`CWK-REVIEW-008`)、承認したコミットからの差分だけがレビューされる (`CWK-REVIEW-014.2`) |
| 4 | レビューの間に、誰かがブランチに push する | `cumin/status/checking` に戻り、もう一度レビューされる (`CWK-REVIEW-005`) |
| 5 | 1ラウンド目の指摘が、提案と元からある問題だけである | `REQUEST_CHANGES` になり (`CWK-REVIEW-011`)、Implementer が提案に返答し (`CWK-REVIEW-021`)、2ラウンド目で `APPROVE` が出る。元からある問題は直されない (`CWK-REVIEW-013`) |

## 未決事項

なし
