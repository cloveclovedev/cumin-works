# レビュー

必須チェックが完了したPull RequestをReviewerがレビューし、指摘をImplementerが直すまでの流れを決める。cuminがReviewerを起動し、レビューをGitHubで確かめ、ラウンドを数え、上限でAssigneeに回すまでである。マージとマージ判断は[マージ](../merge/index.md)、Agent起動に共通のことは[Agent](../agent/index.md)で決める。

## 全体像

実装Issueがレビューでたどる状態を示す。灰色の状態は、ほかのカテゴリで決める。

![レビューの全体像](overview.svg)

図の元ファイル: [overview.puml](overview.puml)

「Reviewerが動いていない」の意味は、[Agent](../agent/index.md)の`CWK-AGENT-001`で決める。

ReviewerとImplementerは、セッションを共有しない。Reviewerが見るのは、GitHubに出ているものとリポジトリの中身だけである。

## 要件

### レビューのAgent起動

#### `CWK-REVIEW-001` レビューを始める

`cumin/status/checking`の実装Issueで、必須チェックが先頭のコミットで完了したとき（`CWK-CHECK-002`）、cuminは状態を`cumin/status/reviewing`に替えてから、Reviewerを起動する。1ラウンド目は新しいセッションで、2ラウンド目からはReviewerの直前のセッションで起動する。

- 検証: `test` 必須チェックが完了すると`cumin/status/reviewing`になり、Reviewerの起動が1回だけ起きる
- 検証: `test` 指摘の修正のあとは、Reviewerの直前のセッションで起動する

---

#### `CWK-REVIEW-002` Reviewerに渡す事実

cuminは、Reviewerに次の事実を渡す。

- 何ラウンド目かと、ラウンドの上限
- 先頭のコミット
- 2ラウンド目からは、前のラウンドでレビューしたコミット
- ReviewerがこのPull Requestを承認したことがあれば、最後に承認したコミット

- 検証: `test` 2ラウンド目に渡す事実に、ラウンド、上限、先頭のコミット、前のラウンドのコミットがある
- 検証: `test` 承認のあとの1ラウンド目には、最後に承認したコミットがある。承認したことがなければ、ない

---

### レビューの完了判断

#### `CWK-REVIEW-003` レビューの完了を判断する

cuminは、`cumin-reviewer`が今の先頭のコミットに`APPROVE`か`REQUEST_CHANGES`のレビューを出したとき、レビューが完了したと判断する。古いコミットへのレビュー、`COMMENT`だけのレビュー、Reviewerの結果は使わない。

- 検証: `test` 古いコミットへの`APPROVE`と、今の先頭のコミットへの`COMMENT`は、レビューがないものとして扱う

---

#### `CWK-REVIEW-004` 指摘を修正する

`cumin/status/reviewing`の実装Issueで、Reviewerが動いておらず、最新のレビューが今の先頭のコミットへの`REQUEST_CHANGES`で、ラウンドが上限に達していないとき、cuminは状態を`cumin/status/implementing`に替えて、Implementerの直前のセッションで指摘の修正を頼む。

- 検証: `test` `REQUEST_CHANGES`のあとで`cumin/status/implementing`になり、Implementerの直前のセッションで起動が1回起きる

---

#### `CWK-REVIEW-005` チェックに戻る

`cumin/status/reviewing`の実装Issueで、Reviewerが動いておらず、次のどちらかが成り立つとき、cuminは状態を`cumin/status/checking`に替える。古いコミットへのレビューも、ラウンドに含めたままにする。

- レビューの間に、先頭のコミットが変わった
- `APPROVE`が出ているが、必須チェックが今の先頭のコミットで完了していない

- 検証: `test` レビューの間にpushされると`cumin/status/checking`に戻り、必須チェックが完了するともう一度レビューされる

---

#### `CWK-REVIEW-006` レビューをやり直す

`cumin/status/reviewing`の実装Issueで、Reviewerが動いておらず、今の先頭のコミットにReviewerのレビューも質問のコメントもないとき、cuminは状態を替えずにReviewerをもう一度起動する。やり直しは、1つの`cumin/status/reviewing`の間に1回だけである。

- 検証: `test` レビューを出さずに終わると、Reviewerがもう一度だけ起動され、状態は`cumin/status/reviewing`のままである

---

#### `CWK-REVIEW-007` レビューを止める

`cumin/status/reviewing`の実装Issueで、Reviewerが動いておらず、次のどれかが成り立つとき、cuminは状態を`cumin/status/awaiting-decision`に替えて、通知する。質問のコメントがないときは、止めた理由をコメントに書く。当たる理由はすべて書く。

- `cumin/status/reviewing`になったあとに、Reviewerの質問のコメントがある。質問ではやり直さない
- やり直しのあとも、今の先頭のコミットにレビューがない
- リスクのラベルがないか、2つ以上ある
- 実装Issueを閉じる開いているPull Requestがない

- 検証: `test` Reviewerが`blocked`を返すと、やり直さずに`cumin/status/awaiting-decision`になる
- 検証: `test` リスクのラベルを外したときと、Pull Requestが閉じられたときは、それぞれの理由のコメントが付いて止まる

---

### ラウンド

#### `CWK-REVIEW-008` ラウンドの数え方

ラウンドは、次の2つの新しいほうよりあとに出た`cumin-reviewer`のレビューの数とする。cuminはラウンドの数を手元に持たない。

- 実装Issueに最後に`cumin/status/ready`が付いたとき
- `cumin-reviewer`が最後に`APPROVE`を出したとき

`APPROVE`のあとで数え直すのは、衝突の解消やAssigneeの`REQUEST_CHANGES`（`CWK-MERGE-004`）で先頭のコミットが変わり、レビューをやり直すからである。

- 検証: `test` `cumin/status/ready`の付け直しのあとと、`APPROVE`のあとで、ラウンドが1から数え直される

---

#### `CWK-REVIEW-009` 原因を整理する

`cumin/status/reviewing`の実装Issueで、Reviewerが動いておらず、最新のレビューが今の先頭のコミットへの`REQUEST_CHANGES`で、ラウンドが上限（`max_review_rounds`、初期値は5）に達し、原因の整理のコメントがまだないとき、cuminは状態を替えずに、Reviewerの直前のセッションで原因の整理を頼む。

- 検証: `test` 上限のラウンドで`REQUEST_CHANGES`が出ると、指摘の修正ではなく、原因の整理の起動が1回起きる

---

#### `CWK-REVIEW-010` ラウンドの上限で止める

`cumin/status/reviewing`の実装Issueで、Reviewerの原因の整理のコメントがPull Requestにあるとき、cuminは状態を`cumin/status/awaiting-decision`に替えて、通知する。

- 検証: `test` 原因の整理のコメントが付くと`cumin/status/awaiting-decision`になり、通知は1回だけである

---

### Reviewerの成果物

#### `CWK-REVIEW-011` レビュー

Reviewerは、今の先頭のコミットにGitHubのレビューを1つ出す。修正が必要な指摘が残っておらず、提案のすべてにImplementerの返答があるときだけ`APPROVE`にし、それ以外は`REQUEST_CHANGES`にする。元からある問題（`CWK-REVIEW-013`）は、この判断に使わない。`COMMENT`だけのレビューにはしない。指摘とまとめは[review.md](../../../../templates/review.md)の形式で書き、まとめにラウンドと上限を書く。

- 検証: `demo` 正しいPull Requestで、指摘がなければ`APPROVE`が出る
- 検証: `demo` 完了の条件を満たしていないPull Requestで`REQUEST_CHANGES`が出て、修正が必要な指摘に場所と理由がある
- 検証: `demo` 提案だけがあるPull Requestで、1ラウンド目は`REQUEST_CHANGES`になり、すべての提案に返答があるラウンドで`APPROVE`になる

---

#### `CWK-REVIEW-012` 修正が必要な指摘

Reviewerは、指摘を、修正が必要なもの`(blocking)`と提案`(non-blocking)`に分ける。修正が必要な指摘には、何が問題か、なぜ直すか、どう直すかを書く。修正が必要な指摘にしてよいのは、次のものだけである。

- 完了の条件を満たしていない
- 動作が正しくない。テストが足りず、正しさを確かめられない
- 不具合の修正が、根本の原因ではなく症状だけを抑えている
- 実装Issueの範囲を超えた変更がある
- 直すべき文書が直されていない
- リポジトリにある指示（`CLAUDE.md`、`AGENTS.md`など）に反している
- セキュリティの問題がある（`CWK-REVIEW-016`）
- 計算の手間が、現実的なデータ量で明らかに問題になる。完了の条件の性能の目標を満たしていない場合も含む
- 読んでも正しさを確かめられないほど入り組んでいる

同じ種類の誤りがいくつもの箇所にあれば、1つの指摘にまとめ、すべての箇所を挙げる。直さなくても正しく動く改善案は、提案にする。書き方の好み、言い回し、片付けだけのことは、提案にもしない。

- 検証: `demo` 正しく動くが書き方に好みの分かれるPull Requestで、その点は指摘されず、`APPROVE`になる
- 検証: `demo` 同じ誤りが3か所にあるPull Requestで、指摘は1つで、3か所が挙げてある
- 検証: `demo` 症状だけを抑える不具合の修正で、根本の原因を直すよう求める指摘が出る

---

#### `CWK-REVIEW-013` 元からある問題

Reviewerは、Pull Requestが持ち込んだのではなく、前からmainブランチにある問題に気づいたら、元からある問題`(pre-existing)`として書く。元からある問題は`APPROVE`を止めず、Implementerは直さず、返答も要らない。マージのあとにcuminがフォローアップノートに書き（[マージ](../merge/index.md)）、受け入れ確認でPlannerが扱いを決める（[受け入れ](../accept/index.md)）。

- 検証: `demo` mainブランチの不具合の近くを変える正しいPull Requestで、その不具合が元からある問題として書かれ、`APPROVE`が出る

---

#### `CWK-REVIEW-014` ラウンドごとに見る範囲

Reviewerは、1ラウンド目でできる限り洗い出し、2ラウンド目からは指摘が直ったかと返答だけを見る。毎回一から見直すと、新しい指摘がなくならないためである。

##### `CWK-REVIEW-014.1` 1ラウンド目

Reviewerは、AgentのCLIに組み込みのコードレビューとセキュリティレビューのskill（Claude Codeでは`code-review`と`security-review`）を実行する。`code-review`の深さは`CWK-REVIEW-015`で決める。GitHubに投稿する、ファイルを変える、クラウドで動く使い方はしない。skillが挙げた候補は、コードを読んで確かめてから指摘にする。skillが見ないこと（完了の条件、範囲、文書、リポジトリにある指示、専門知識のファイルのセキュリティの観点）は、Reviewerが確かめる。skillがないか失敗したときは、同じ観点を自分で確かめる。

- 検証: `demo` 1ラウンド目で2つのskillが実行され、まとめに実行したskillが書いてある

---

##### `CWK-REVIEW-014.2` 承認のあとの1ラウンド目

ReviewerがこのPull Requestを承認したことがあれば、最後に承認したコミット（`CWK-REVIEW-002`）から今の先頭のコミットまでの差分だけを、1ラウンド目と同じ深さで見る。skillもその差分に対して実行する。承認したことがなければ、全体を見る。マージ判断での`REQUEST_CHANGES`や衝突の解消のたびに、全体をレビューし直さないためである。

- 検証: `demo` 承認のあとに新しいコミットが入ったPull Requestで、承認したコミットからの差分だけを見て、skillもその差分に対して実行する

---

##### `CWK-REVIEW-014.3` 2ラウンド目から

Reviewerは、前の指摘と返答、前のラウンドのコミットから今の先頭のコミットまでの差分だけを見る。新しく指摘してよいのは、直し方の誤り、納得できない返答、その差分の中の動作の誤りとセキュリティの問題だけである。同じ指摘を繰り返さず、skillも実行しない。

- 検証: `demo` 2ラウンド目で、前の指摘が直り、提案のすべてに返答があることを確かめて`APPROVE`を出す
- 検証: `demo` 2ラウンド目で、差分の外の問題に気づいても、指摘を足さない

---

#### `CWK-REVIEW-015` レビューの深さ

Reviewerは、リスクのラベルに合わせて、コードレビューのskillの深さ（Claude Codeではeffort）を選ぶ。対応は、専門知識のファイルに書く。初期の専門知識`software-engineering`では、`cumin/risk/low`と`cumin/risk/medium`は`high`、`cumin/risk/high`は`xhigh`である。

- 検証: `inspection` Reviewerが受け取る専門知識のファイルに、リスクのラベルごとの深さがある
- 検証: `demo` `cumin/risk/high`の実装Issueの1ラウンド目で、`code-review`が`xhigh`で実行される

---

#### `CWK-REVIEW-016` セキュリティの観点とチェックに任せること

Reviewerは、専門知識のファイルにある観点で、セキュリティの問題を確かめる。秘密の値の検出、脆弱性のある依存、静的解析、書式、命名、複雑さのしきい値のように機械で確かめられることは、必須チェックに任せ、Reviewerは見ない。

- 検証: `inspection` Reviewerが受け取る専門知識のファイルに、セキュリティの観点と、チェックに任せることがある

---

#### `CWK-REVIEW-017` リポジトリのレビューの指針

対象のリポジトリの最上位に`REVIEW.md`があれば、Reviewerはそれに従う。`REVIEW.md`には、Maintainerが、そのリポジトリで何が失敗にあたるか、見なくてよいパス、指摘の根拠の示し方などを書く。

- 検証: `demo` 「`generated/`は見ない」と書いた`REVIEW.md`のあるリポジトリで、`generated/`の中の変更に指摘が出ない

---

#### `CWK-REVIEW-018` 原因の整理のコメント

原因の整理を頼まれたReviewer（`CWK-REVIEW-009`）は、Pull RequestにAssignee向けのコメントを1つ書く。形式は[decision-request.md](../../../../templates/decision-request.md)に従い、決めてほしいこと、何が決まっていないために指摘が直らないかとそれをどこに書くべきか、指摘ごとのReviewerとImplementerの立場、選べる決め方と得失、勧める案を書く。

- 検証: `demo` 上限のラウンドで指摘が残ると、1行目に決めてほしいことがあり、選択肢と勧める案のあるコメントが付く

---

#### `CWK-REVIEW-019` 直さない

Reviewerは、コードを変えず、pushせず、Pull Requestをマージせず、閉じず、Issueの本文を書き換えない。pushは権限でも止まる（`CWK-ACCESS-012`）。

- 検証: `demo` レビューのあとで、Pull Requestのコミット、状態、Issueの本文が変わっていない

---

### Implementerの指摘の修正

#### `CWK-REVIEW-020` 指摘への返答

Implementerは、修正が必要な指摘と提案のすべてに返答する。元からある問題には返答しなくてよい。形式は[review-reply.md](../../../../templates/review-reply.md)に従う。

- 検証: `demo` 指摘の修正のあとで、修正が必要な指摘と提案のすべてに返答がある

---

#### `CWK-REVIEW-021` 提案の扱い

Implementerは、提案を次の3つに分け、1行の理由を付けて返答する。

- このPull Requestで直す: 範囲の中で直せるもの。直して`Fixed`と返答する
- 判断が要る: 変えずに`Deferred`と返答する
- 対応しない: 変えずに`Not changed`と返答する

提案を`Follow-up`に写さない。直されなかった提案は、マージのあとにcuminがフォローアップノートに書く（[マージ](../merge/index.md)）。

- 検証: `demo` 範囲の中の小さな提案は`Fixed`になり、範囲の外の提案は`Deferred`か`Not changed`になって、`Follow-up`に写されない

---

### Reviewerが判断を求めるとき

#### `CWK-REVIEW-022` 推測でレビューしない

Reviewerは、次のどれかに当たったら、レビューを出さずに`blocked`を返す。cuminは、やり直さずに止める（`CWK-REVIEW-007`）。

##### `CWK-REVIEW-022.1` 合わないリスク

リスクのラベルが実際の変更と合わないときは、止まる。例えば`cumin/risk/low`なのに、データベースのマイグレーションを含む。認証、暗号、セッションの処理を変える変更は、リスクの基準の初期値では`cumin/risk/high`である。リスクを決め直すのはAssigneeなので、Implementerには回さない。

- 検証: `demo` マイグレーションを含む`cumin/risk/low`の実装Issueでレビューを頼むと、レビューは出ず、リスクが合わないことが理由に書いてある

---

##### `CWK-REVIEW-022.2` 関係のないPull Request

Pull Requestの内容が、実装Issueとほとんど関係がないときは、止まる。

- 検証: `demo` 関係のない変更のPull Requestでレビューを頼むと、結果は`blocked`である

---

##### `CWK-REVIEW-022.3` 曖昧な完了の条件

完了の条件が曖昧で、満たしたかを判定できないときは、止まる。

- 検証: `demo` 完了の条件が曖昧な実装Issueでレビューを頼むと、判定できない条件が理由に書いてある

---

## 受け入れテスト

| # | 場面 | 期待する結果 |
|---|---|---|
| 1 | Reviewerが`REQUEST_CHANGES`を出し、Implementerが直し、2ラウンド目で`APPROVE`が出る | レビューが始まり（`CWK-REVIEW-001`）、修正を頼み（`CWK-REVIEW-004`）、必須チェックのあと、2ラウンド目が前の指摘と返答と差分だけを見て承認する（`CWK-REVIEW-014.3`）|
| 2 | 上限のラウンドでも指摘が残る | 原因の整理を頼み（`CWK-REVIEW-009`）、コメントが付くと`cumin/status/awaiting-decision`になって通知が1回届く（`CWK-REVIEW-010`、`CWK-REVIEW-018`）|
| 3 | 承認のあとに衝突が起き、Implementerが解消する | ラウンドが1から数え直され（`CWK-REVIEW-008`）、承認したコミットからの差分だけがレビューされる（`CWK-REVIEW-014.2`）|
| 4 | レビューの間に、誰かがブランチにpushする | `cumin/status/checking`に戻り、もう一度レビューされる（`CWK-REVIEW-005`）|
| 5 | 1ラウンド目の指摘が、提案と元からある問題だけである | `REQUEST_CHANGES`になり（`CWK-REVIEW-011`）、Implementerが提案に返答し（`CWK-REVIEW-021`）、2ラウンド目で`APPROVE`が出る。元からある問題は直されない（`CWK-REVIEW-013`）|

## 未決事項

なし
