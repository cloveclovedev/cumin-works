# マージ

Reviewer が承認した Pull Request を、マージし、実装Issueを閉じるまでの流れを決める。リスクに合わせてマージ判断を Assignee に求めること、Assignee の承認と指摘、マージ、実装Issueを閉じること、フォローアップノートまでである。承認の前のレビューは [レビュー](../review/index.md)、`cumin/status/checking` と `cumin/status/awaiting-merge-decision` からの衝突の解消は [実装](../impl/index.md)、受け入れ確認は [受け入れ](../accept/index.md) で決める。

## 全体像

実装Issueがマージでたどる状態を示す。灰色の状態は、ほかのカテゴリで決める。

![マージの全体像](overview.svg)

図の元ファイル: [overview.puml](overview.puml)

このページで「Reviewer が動いていない」とは、この cumin が、その実装Issueの Reviewer の実行を今持っていないことである。

マージそのものは Agent を起動しないので、利用制限で Agent起動を止めている間も、cumin を止める途中でも、`cumin/status/merging` の実装Issueはマージする。衝突の解消のように Agent を起動するアクションだけが待つ ([利用制限](../limit/index.md))。

## 要件

### マージに進む

#### `CWK-MERGE-001` マージを始める

`cumin/status/reviewing` の実装Issueで、Reviewer が動いておらず、次のすべてが成り立つとき、cumin は状態を `cumin/status/merging` に替える。マージは `cumin/status/merging` の中で行う (`CWK-MERGE-006`)。

- Reviewer の最新のレビューが、今の先頭のコミットへの `APPROVE` である
- 必須チェックが、今の先頭のコミットですべて通っている
- リスクのラベルが `cumin/risk/low` である

- 検証: `test` `cumin/risk/low` の実装Issueで、Reviewer が今の先頭のコミットを承認し、必須チェックが通っていると、実装Issueが `cumin/status/merging` になる

---

#### `CWK-MERGE-002` マージ判断を求める

`cumin/status/reviewing` の実装Issueで、`CWK-MERGE-001` と同じ条件のうち、リスクのラベルだけが `cumin/risk/medium` か `cumin/risk/high` であるとき、cumin は状態を `cumin/status/awaiting-merge-decision` に替える。そして、Pull Request で実装Issueの Assignee にレビューを頼み、マージ判断が要ることを通知する。

レビューを頼むのに失敗しても、実装Issueを止めない。ログに残すだけにする。通知と状態ラベルで、Assignee は判断を待つ Pull Request を見つけられるためである。

- 検証: `test` `cumin/risk/medium` の実装Issueで Reviewer が承認すると、マージせずに `cumin/status/awaiting-merge-decision` になり、Assignee へのレビューの依頼と通知が1回ずつ起きる
- 検証: `test` レビューの依頼が失敗しても、実装Issueは `cumin/status/awaiting-merge-decision` のままで、`cumin/status/awaiting-decision` にならない

---

### マージ判断

Assignee は、Pull Request を GitHub のレビューで判断する。cumin が数えるのは、実装Issueの今の Assignee が出したレビューのうち、`COMMENT` だけのものを除いた最新のものである。ほかの Maintainer のレビューは数えない ([権限](../access/index.md) の `CWK-ACCESS-002`)。Assignee は、コメントを書いて実装Issueに `cumin/status/ready` を付け直し、実装をやり直させることもできる ([実装](../impl/index.md) の `CWK-IMPL-001`)。

#### `CWK-MERGE-003` Assignee の承認でマージを始める

`cumin/status/awaiting-merge-decision` の実装Issueで、Assignee の最新のレビューが今の先頭のコミットへの `APPROVE` で、必須チェックが今の先頭のコミットですべて通っているとき、cumin は状態を `cumin/status/merging` に替える。古いコミットへの承認は数えない。衝突の解消などで新しいコミットが入ったら、Assignee はもう一度承認する。

- 検証: `test` Assignee が今の先頭のコミットを承認すると、実装Issueが `cumin/status/merging` になる
- 検証: `test` 古いコミットへの承認、Assignee でない Maintainer の承認、bot の承認、あとから同じ Assignee の `REQUEST_CHANGES` で覆された承認では、`cumin/status/merging` にならない

---

#### `CWK-MERGE-004` Assignee の指摘を修正する

`cumin/status/awaiting-merge-decision` の実装Issueで、Assignee の最新のレビューが今の先頭のコミットへの `REQUEST_CHANGES` で、そのレビューが、実装Issueが最後に `cumin/status/awaiting-merge-decision` になったあとに出されているとき、cumin は状態を `cumin/status/implementing` に替えて、Implementer の直前のセッションで Assignee の指摘の修正を頼む。

- 修正のあとは、必須チェック ([実装](../impl/index.md))、Reviewer のレビュー ([レビュー](../review/index.md)) を通り、`CWK-MERGE-002` でもう一度 Assignee の判断を待つ。
- この修正は、レビューのラウンドにも、チェックの修正の回数にも数えず、回数を0にも戻さない。`REQUEST_CHANGES` を出すのは毎回 Assignee なので、止まらない往復にならないためである。レビューのラウンドは、Reviewer の最後の `APPROVE` のあとから数え直す ([レビュー](../review/index.md) の `CWK-REVIEW-008`)。
- 1つの `REQUEST_CHANGES` で修正を頼むのは1回だけである。Implementer がコミットせずに答えたときは、先頭のコミットが変わらず、`REQUEST_CHANGES` はそのコミットに残るが、実装Issueが `cumin/status/awaiting-merge-decision` に戻ったあとのレビューではないので、この遷移は起きない。Assignee は答えを読んで、承認するか、もう一度 `REQUEST_CHANGES` を出す。

- 検証: `test` Assignee が今の先頭のコミットに `REQUEST_CHANGES` を出すと、実装Issueが `cumin/status/implementing` になり、Implementer の直前のセッションで Agent起動が1回起きる。レビューのラウンドとチェックの修正の回数は変わらない
- 検証: `test` Implementer がコミットせずに答えたあと、実装Issueがもう一度 `cumin/status/awaiting-merge-decision` になっても、同じ `REQUEST_CHANGES` で2回目の修正は頼まない
- 検証: `test` Assignee でない Maintainer と bot の `REQUEST_CHANGES` では、何もしない

---

#### `CWK-MERGE-005` マージ判断と衝突の順番

`cumin/status/awaiting-merge-decision` の実装Issueでは、1回の定期確認で、`CWK-MERGE-003`、`CWK-MERGE-004`、衝突の解消 ([実装](../impl/index.md) の `CWK-IMPL-015`) の順に判定し、最初に成り立ったものだけを行う。衝突した先頭のコミットに Assignee の判断があれば、その判断を先に進めるためである。承認なら `cumin/status/merging` で衝突を解消し (`CWK-MERGE-009`)、`REQUEST_CHANGES` なら指摘の修正のあとの `cumin/status/checking` で衝突を解消する。

- 検証: `test` 衝突していて、Assignee が今の先頭のコミットを承認した実装Issueは、`cumin/status/implementing` ではなく `cumin/status/merging` になる

---

### マージ

#### `CWK-MERGE-006` マージの条件

cumin は、`cumin/status/merging` の実装Issueの Pull Request を、次のすべてが成り立つときだけマージする。`cumin/status/merging` のラベルが付いていることは、条件の代わりにならない。cumin は、マージを送るたびに条件を確かめ直す。

- 必須チェックが、今の先頭のコミットですべて通っている
- リスクのラベルが1つだけ付いている
- Reviewer の最新のレビューが、今の先頭のコミットへの `APPROVE` である
- リスクが `cumin/risk/medium` か `cumin/risk/high` なら、Assignee の最新のレビューも、今の先頭のコミットへの `APPROVE` である

- 検証: `test` `cumin/status/merging` を手で付けた、Assignee の承認のない `cumin/risk/medium` の実装Issueは、マージしない

---

#### `CWK-MERGE-007` マージを送る

`cumin/status/merging` の実装Issueで、Pull Request がマージされておらず、マージの条件 (`CWK-MERGE-006`) が成り立ち、GitHub が衝突していると返していないとき、cumin はマージを送る。

- マージの方法は、Host の設定 (`merge_method`、初期値は squash) に従う。
- マージに渡す先頭のコミットは、承認されたコミットである。承認のあとに push されたコミットは、マージしない。
- マージの答えが届かなかったときも、実装Issueは `cumin/status/merging` のまま残る。次の定期確認が、Pull Request がマージ済みかを読み、マージ済みなら実装Issueを閉じ (`CWK-MERGE-012`)、そうでなければもう一度マージを送る。同じマージを2回行うことはない。
- 1回の定期確認で2つ以上の Pull Request をマージするときは、1つマージするごとに数秒待つ。GitHub が main ブランチを更新する時間を置くためである。

- 検証: `test` マージの答えが届かなかった Pull Request で、次の定期確認がマージ済みと読むと、マージを送り直さずに実装Issueを閉じる
- 検証: `test` マージの前に承認のないコミットが push されると、そのコミットはマージされない

---

#### `CWK-MERGE-008` チェックに戻る

`cumin/status/merging` の実装Issueで、Pull Request がマージされておらず、マージの条件 (`CWK-MERGE-006`) が成り立たないとき、cumin は状態を `cumin/status/checking` に替える。マージは送らない。

- 検証: `test` `cumin/status/merging` になったあとで、Assignee が `REQUEST_CHANGES` を出すか、先頭のコミットが変わると、マージせずに `cumin/status/checking` になる

---

#### `CWK-MERGE-009` マージの前の衝突を解消する

`cumin/status/merging` の実装Issueで、マージの条件 (`CWK-MERGE-006`) が成り立ち、GitHub が Pull Request を main ブランチと衝突していると返したとき (GraphQL の `mergeable` が `CONFLICTING`)、または、衝突を理由にマージを断ったとき、cumin は状態を `cumin/status/implementing` に替えて、Implementer の直前のセッションで衝突の解消を頼む。衝突していると分かっている Pull Request には、マージを送らない。断られたあとは、送り直さない。

衝突の解消は、チェックの修正の回数に数えない ([実装](../impl/index.md) の `CWK-IMPL-015` と同じ)。解消のあとは、必須チェックとレビューを通り、リスクに合わせてもう一度 `CWK-MERGE-001` か `CWK-MERGE-002` に進む。

- 検証: `test` 承認されたあとに衝突した Pull Request では、マージを送らずに、Implementer に衝突の解消を1回だけ頼む

---

#### `CWK-MERGE-010` main ブランチが変わってマージを断られたとき

GitHub が、main ブランチが変わったことを理由にマージを断ったとき (405、`Base branch was modified`)、cumin は実装Issueを止めず、`cumin/status/merging` のまま、次の定期確認でもう一度マージを送る。ほかの Pull Request のマージの直後に起きる、すぐに直る断り方だからである。

- 検証: `test` 405 でマージを断られた実装Issueは、`cumin/status/merging` のままで、次の定期確認でマージされる

---

#### `CWK-MERGE-011` マージを止める

`cumin/status/merging` の実装Issueで、次のどちらかが起きたとき、cumin は状態を `cumin/status/awaiting-decision` に替え、理由をコメントに書いて、通知する。

- GitHub が、衝突 (`CWK-MERGE-009`) でも、main ブランチが変わったこと (`CWK-MERGE-010`) でもない理由で、マージを断った。やり直しても直らない理由だからである
- マージのあとで、実装Issueを閉じられない (`CWK-MERGE-012`)

- 検証: `test` 必須チェックの不足など、ほかの理由でマージを断られた実装Issueは、理由のコメントが付いて `cumin/status/awaiting-decision` になり、通知が1回届く

---

### 実装Issueを閉じる

#### `CWK-MERGE-012` マージした実装Issueを閉じる

`cumin/status/merging` の実装Issueで、GitHub が Pull Request をマージ済みと返したとき、cumin は、GitHub が実装Issueを閉じていなければ閉じる。GitHub は、手で付けたリンクのある Pull Request をマージしても、Issue を閉じないことがあるためである。

cumin が実装Issueを閉じるのは、このときだけである。`cumin/status/merging` を出たあとは閉じないので、Maintainer が開き直した実装Issueは開いたままになる。

- 検証: `test` マージのあとで GitHub が実装Issueを閉じないと、cumin が1回だけ閉じる。Maintainer がそれを開き直しても、あとの定期確認では閉じない

---

#### `CWK-MERGE-013` 人がマージしたとき

main ブランチの ruleset を迂回できる Maintainer (admin) は、Pull Request を自分でマージしてもよい。そのあとの扱いは、cumin がマージしたときと同じである (`CWK-MERGE-014`)。ただし、GitHub が実装Issueを閉じなければ、その Maintainer が閉じる。cumin は、`cumin/status/merging` の外では実装Issueを閉じない (`CWK-MERGE-012`)。

- 検証: `test` admin の Maintainer が `cumin/status/awaiting-merge-decision` の Pull Request をマージし、実装Issueを閉じると、cumin がマージしたときと同じフォローアップノートが付く

---

### フォローアップノート

Implementer が範囲の外だと判断した作業と、直されなかった Reviewer の提案と、元からある問題は、マージされると Pull Request の中に埋もれる。cumin は、これをフォローアップノートとして要求Issueに書く。受け入れ確認で、Planner はフォローアップノートから残りの作業を扱う ([受け入れ](../accept/index.md))。

#### `CWK-MERGE-014` フォローアップノートを書く

実装Issueが閉じていて、それを閉じるよう結び付いた Pull Request がマージ済みで、その Pull Request のフォローアップノートがまだないとき、cumin はフォローアップノートを、要求Issueに1つのコメントとして書く。誰が実装Issueを閉じたか (GitHub、cumin、Maintainer) は問わない。どの状態でも行い、状態を変えない。形式は [follow-up-note.md](../../../../templates/follow-up-note.md) に従う。

- 検証: `test` `Follow-up` に文章のある Pull Request がマージされると、要求Issueに、決められた形式のフォローアップノートが1つ付く

---

#### `CWK-MERGE-015` フォローアップノートに書くもの

cumin は、Agent の判断を使わず、決まった形式から次のものを機械的に拾う。

- Pull Request の説明の `Follow-up` の節の文章。手を加えずに写す
- Reviewer の提案 (`(non-blocking)`) のうち、Implementer の返答が `Fixed` でないもの。返答が `Deferred` か `Not changed` のものと、返答のないものである ([レビュー](../review/index.md) の `CWK-REVIEW-021`)
- 元からある問題 (`(pre-existing)`、[レビュー](../review/index.md) の `CWK-REVIEW-013`)

拾うものが何もなければ、フォローアップノートを書かない。

- 検証: `test` `Follow-up` が None で、提案がすべて `Fixed` になり、元からある問題のない Pull Request では、フォローアップノートが付かない
- 検証: `test` `Deferred` の提案と、元からある問題が1つずつある Pull Request では、その2つがフォローアップノートに載る

---

#### `CWK-MERGE-016` 1つの Pull Request に1つ

フォローアップノートは、1つの Pull Request について1つだけ書く。cumin はコメントに目印を埋め込み、再起動しても同じノートを二重に書かない。手元には何も持たず、ノートがあるかは GitHub から読む。

- 検証: `test` フォローアップノートを書いたあとで cumin を再起動しても、同じフォローアップノートは増えない

---

#### `CWK-MERGE-017` 開いている要求Issueだけに書く

フォローアップノートを書くのは、要求Issueが開いている間だけである。閉じた要求Issueには書かない。

- 検証: `test` 閉じた要求Issueの sub-issue の Pull Request がマージされても、フォローアップノートは書かない

---

#### `CWK-MERGE-018` 受け入れ確認の前にそろえる

要求Issueの sub-issue がすべて閉じたとき、cumin は、閉じた sub-issue のフォローアップノートを書き終えてから、受け入れ確認を頼む ([受け入れ](../accept/index.md))。Planner が受け入れ確認を始めるときに、フォローアップノートがそろっているようにするためである。

- 検証: `test` 最後の sub-issue の Pull Request がマージされた定期確認で、フォローアップノートが付く前に受け入れ確認の Agent起動が起きない

---

#### `CWK-MERGE-019` Issue にしない

フォローアップノートは記録である。cumin は、フォローアップノートから Issue を作らない。

- 検証: `test` フォローアップノートを書いたあとで、Issue の数が増えていない

---

## 受け入れテスト

| # | 場面 | 期待する結果 |
|---|---|---|
| 1 | `cumin/risk/low` の Pull Request を Reviewer が承認する | `cumin/status/merging` になり (`CWK-MERGE-001`)、承認されたコミットがマージされ (`CWK-MERGE-007`)、実装Issueが閉じ (`CWK-MERGE-012`)、残りの作業があればフォローアップノートが1つ付く (`CWK-MERGE-014`、`CWK-MERGE-015`) |
| 2 | `cumin/risk/medium` の Pull Request を Reviewer が承認し、Assignee が承認する | `cumin/status/awaiting-merge-decision` になって通知が届き (`CWK-MERGE-002`)、Assignee の承認で `cumin/status/merging` になってマージされる (`CWK-MERGE-003`、`CWK-MERGE-006`) |
| 3 | マージ判断で Assignee が `REQUEST_CHANGES` を出し、Implementer が直す | Implementer の直前のセッションで修正を頼み (`CWK-MERGE-004`)、チェックと、承認したコミットからの差分のレビュー (`CWK-REVIEW-014.2`) を通って、もう一度 `cumin/status/awaiting-merge-decision` になる。レビューのラウンドとチェックの修正の回数は増えない |
| 4 | 承認されたあと、ほかの Pull Request のマージで衝突する | マージを送らずに衝突の解消を頼み (`CWK-MERGE-009`)、チェックとレビューを通ってもう一度マージに進む |
| 5 | 同じ定期確認で、2つの Pull Request をマージする | 1つマージするごとに数秒待ち (`CWK-MERGE-007`)、405 で断られた Pull Request は次の定期確認でマージされる (`CWK-MERGE-010`) |
| 6 | 最後の sub-issue の Pull Request がマージされる | フォローアップノートが付いてから、受け入れ確認を頼む (`CWK-MERGE-018`) |

## 未決事項

- 「Reviewer が動いていない」の意味は、ほかのページと同じく、[Agent](../agent/index.md) か [定期確認](../poll/index.md) に移す。
- マージの間に待つ秒数 (今のコードでは5秒) を設定にするかは、HOST の設定を書くときに決める。
