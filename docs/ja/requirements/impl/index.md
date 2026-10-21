# 実装

実装Issueを Pull Request にまとめるまでの流れを決める。cumin が Implementer を起動し、Pull Request を GitHub で確かめて、必須チェックを待つ状態に替えるまでである。必須チェック、チェックの失敗と衝突の修正、保護されたパスのチェックは [チェック](../check/index.md)、レビューは [レビュー](../review/index.md)、マージとマージ判断は [マージ](../merge/index.md)、Agent起動に共通のことは [Agent](../agent/index.md) で決める。

## 全体像

実装Issueが実装でたどる状態を示す。灰色の状態は、ほかのカテゴリで決める。

![実装の全体像](overview.svg)

図の元ファイル: [overview.puml](overview.puml)

「Implementer が動いていない」の意味は、[Agent](../agent/index.md) の `CWK-AGENT-001` で決める。

## 要件

### 実装の Agent起動

#### `CWK-IMPL-001` 実装を始める

`cumin/status/ready` の付いた実装Issueが次のすべてを満たすとき、cumin は状態を `cumin/status/implementing` に替えてから、新しいセッションで Implementer を起動する。実装Issueを閉じる Pull Request がすでにあれば、その続きから進めさせる。

- その `cumin/status/ready` を、cumin が扱う ([権限](../access/index.md) の `CWK-ACCESS-004`)
- 実装Issueの blocked by の Issue が、すべて閉じている (`CWK-IMPL-002`)。または、マージ判断を待つ Pull Request のブランチから始める条件を満たす (`CWK-IMPL-015.1`)
- cumin に空きがある
- `cumin/type/owner-task` が付いていない ([ラベル](../label/index.md) の `CWK-LABEL-004`)

- 検証: `test` 4つを満たす実装Issueに、Implementer の Agent起動が新しいセッションで1回だけ起き、起動の前に状態が `cumin/status/implementing` になっている
- 検証: `test` Pull Request がすでにある実装Issueでは、新しい Pull Request を作らせず、同じ Pull Request の続きから進めさせる

---

#### `CWK-IMPL-002` 先の実装Issueを待つ

blocked by の Issue が1つでも開いている実装Issueには、cumin は着手しない。例外は、開いている blocked by の Issue が Assignee のマージ判断を待っているときで、その Pull Request のブランチから始める (`CWK-IMPL-015`)。

- 検証: `test` blocked by の実装Issueが `cumin/status/implementing` の間は着手せず、閉じたあとの定期確認で着手する

---

#### `CWK-IMPL-003` ブランチの名前

cumin は、実装Issueごとにブランチの名前を決めて、Implementer に渡す。名前は `cumin/<実装Issueの番号>-<短い説明>` の形である。

- 検証: `test` Implementer の Agent起動で渡す事実に、この形のブランチの名前がある

---

#### `CWK-IMPL-004` 回数を数え直す

`CWK-IMPL-001` で `cumin/status/ready` から `cumin/status/implementing` に移るたびに、cumin はチェックの修正の回数 ([チェック](../check/index.md) の `CWK-CHECK-004`) を0に戻す。Maintainer が介入したあとは Agent のセッションも新しくなるので、上限も数え直す。

- 検証: `test` チェックの修正を上限まで頼んで止まった実装Issueに、Maintainer が `cumin/status/ready` を付け直すと、チェックの修正をまた上限まで頼める

---

### 実装の完了判断

#### `CWK-IMPL-005` 実装の完了を判断する

Implementer が動いていないとき、cumin は Pull Request を GitHub で確かめる。cumin は、その実装Issueのために決めたブランチ (`CWK-IMPL-003`) に Pull Request が開いていて、作成者が `cumin-implementer` で、先頭のコミットが push されているとき、実装が完了したと判断する。この判断では、Pull Request をブランチと作成者で見つける。GitHub が本文の `Closes #<番号>` からリンクを作らないことがあるためである。Implementer が返した結果は、判断に使わない。

- 検証: `test` 本文の `Closes` からリンクが作られていない Pull Request でも、ブランチと作成者が合えば実装が完了したと判断する。ほかの作成者の Pull Request では、完了したと判断しない

---

#### `CWK-IMPL-006` チェックを待つ

`cumin/status/implementing` の実装Issueで、Implementer が動いておらず、実装が完了したとき、cumin は状態を `cumin/status/checking` に替える。実装Issueにその Pull Request を閉じるリンクがなければ、`cumin-core` がリンクを付け、付いたことを読み直して確かめてから替える。GitHub がすでにリンクを作っていれば、何も付けない。ほかのアクションは、このリンクで実装Issueの Pull Request を見つける。

- 検証: `test` リンクのない Pull Request で、cumin がリンクを付け、実装Issueが `cumin/status/checking` になる。リンクがすでにあれば、付け直さない

---

#### `CWK-IMPL-007` 実装をやり直す

`cumin/status/implementing` の実装Issueで、Implementer が動いておらず、実装が完了しておらず、Implementer の質問のコメントがないとき、cumin は Implementer をもう一度起動して、続きから進めさせる。やり直すのは、1つの `cumin/status/implementing` の間に1回だけである。状態は替えない。

- 検証: `test` Pull Request を開かずに終わった実装のあとで、Implementer の Agent起動がもう一度だけ起き、状態は `cumin/status/implementing` のままである

---

#### `CWK-IMPL-008` 実装を止める

`cumin/status/implementing` の実装Issueで、Implementer が動いておらず、次のどれかが成り立つとき、cumin は状態を `cumin/status/awaiting-decision` に替えて、通知する。質問のコメントでないときは、止めた理由を実装Issueのコメントに書く。

- Implementer の質問のコメントが、実装Issueが `cumin/status/implementing` になったあとに書かれている。Implementer の質問では、やり直さない。実装に要る要件が足りないことが多く、Maintainer が Issue を書き直す必要があるためである
- やり直し (`CWK-IMPL-007`) のあとも、実装が完了していない
- 衝突の解消 ([チェック](../check/index.md) の `CWK-CHECK-007`) を頼んだあとも、先頭のコミットが頼む前のままである。先頭のコミットの時刻と、実装Issueに最新の `cumin/status/implementing` が付いた時刻を比べて決める

- 検証: `test` Implementer が `blocked` を返すと、やり直さずに `cumin/status/awaiting-decision` になる
- 検証: `test` やり直しのあとも実装が完了しないと、理由のコメントが付いて `cumin/status/awaiting-decision` になる
- 検証: `test` 衝突の解消のあとも先頭のコミットが変わらないと、衝突の解消を繰り返さずに `cumin/status/awaiting-decision` になる

---

### マージ判断を待つ Pull Request のブランチから始める

blocked by の Issue の Pull Request が Assignee のマージ判断を待つ間、それに依存する実装Issueを止めないために、cumin はそのブランチから次の実装を始める。その Pull Request のマージを待つことと、マージのあとのことは [マージ](../merge/index.md) で決める。

#### `CWK-IMPL-015` マージ判断を待つ Pull Request のブランチから始める

##### `CWK-IMPL-015.1` 着手の条件

`cumin/status/ready` の実装Issueで、blocked by の Issue が閉じていることのほかは `CWK-IMPL-001` の条件を満たし、次のすべてが成り立つとき、cumin は、blocked by の Issue の Pull Request のブランチから実装を始める。

- 開いている blocked by の Issue が1つだけで、`cumin/status/awaiting-merge-decision` である。Reviewer が承認する前の Pull Request からは始めない
- その Pull Request の base が main ブランチである。これで、始めるのは1段だけになる。深さを選べるようにすることは [バックログ](../backlog.md) にある
- 対象のリポジトリが、マージのあとにブランチを自動で消す (`deleteBranchOnMerge`)。GitHub は、ブランチが消えたときにだけ、そのブランチを base にする Pull Request の base を main ブランチに替える
- 対象のリポジトリの `.cumin/config.toml` で `start_on_awaiting_merge_decision = true` にしている。初期値は `false` である

有効にする前に、Maintainer は、必須チェックのワークフローが、base が main ブランチでない Pull Request でも動くことを確かめる。動かないと、チェックが来ずに止まる ([チェック](../check/index.md) の `CWK-CHECK-006`)。

- 検証: `test` 4つを満たせば着手し、1つでも欠ければ着手しない (blocked by の Issue が `cumin/status/reviewing`、その base が main ブランチでない、`deleteBranchOnMerge` が偽、設定が `false`、開いている blocked by が2つ)

---

##### `CWK-IMPL-015.2` ブランチと base

cumin は Implementer に blocked by の Issue のブランチを渡し、Implementer はそこから新しいブランチを切って、Pull Request の base もそのブランチにする。差分に、この実装Issueの変更だけを出すためである。cumin は、base が main ブランチでないことで、この Pull Request を見分ける。

- 検証: `test` Agent起動で渡す事実に、blocked by の Issue のブランチがある
- 検証: `demo` 開いた Pull Request の base が blocked by の Issue のブランチで、差分にこの実装Issueの変更だけがある

---

##### `CWK-IMPL-015.3` blocked by の Issue の変更は追わない

blocked by の Issue の Pull Request が変わっても、cumin はこの実装Issueのブランチを更新せず、衝突したときだけ衝突の解消 ([チェック](../check/index.md) の `CWK-CHECK-007`) を頼む。変わるたびに更新すると、そのたびに実装とレビューが1回ずつ要るためである。

- 検証: `test` blocked by の Issue の Pull Request に新しいコミットが入っても、Agent起動は起きない。衝突したときだけ、衝突の解消を頼む

---

### Implementer の成果物

#### `CWK-IMPL-009` Pull Request

Implementer は、1つの実装Issueにつき、渡された名前のブランチに Pull Request を1つ開く。説明は [pull-request.md](../../../../templates/pull-request.md) の形式で書き、本文に `Closes #<実装Issueの番号>` を書く。続きや修正のときは、新しい Pull Request を作らず、同じ Pull Request にコミットを積む。

- 検証: `demo` `cumin/status/ready` の実装Issueで実装すると、渡した名前のブランチに Pull Request が1つ開き、本文に `Closes #<番号>` がある
- 検証: `demo` チェックの修正と、マージ判断で Assignee が出した `REQUEST_CHANGES` のあとの続きで、新しい Pull Request は作られず、同じ Pull Request にコミットが積まれる

---

#### `CWK-IMPL-010` 範囲の外の作業

Implementer は、実装Issueに書かれていない範囲に手を広げない。自分で気づいた範囲の外の作業は、Pull Request の説明の `Follow-up` に書き、Issue は作らない。マージのあとに、cumin がフォローアップノートとして要求Issueに書く ([マージ](../merge/index.md))。

- 検証: `inspection` レビューで、Pull Request の変更が実装Issueの範囲の中にあり、範囲の外の作業が `Follow-up` にあることを確かめる

---

#### `CWK-IMPL-011` Pull Request の図

Implementer は、Pull Request の「Where this fits」の図を直すときは、直した図をブランチ `cumin/diagrams` に新しいファイルとして足す。Pull Request のブランチには図のファイルを置かない。ただし、設計文書の図を変えるときは、Pull Request のブランチで変える。

- 検証: `demo` 実装で位置づけが変わった Pull Request で、直した図が `cumin/diagrams` にあり、Pull Request のブランチには、設計文書の図のほかに図のファイルがない

---

#### `CWK-IMPL-012` Issue を変えない

Implementer は、実装Issueと要求Issueの本文を書き換えない。

- 検証: `demo` 実装のあとで、実装Issueと要求Issueの本文が変わっていない

---

#### `CWK-IMPL-013` 待たずに終わる

Implementer は、Pull Request を開いて push したら、レビューもチェックも待たずに実行を終える。チェックとレビューを待つのは cumin である。

- 検証: `inspection` Implementer の role のファイルが、チェックとレビューを待たないよう求めていることを確かめる

---

### Implementer が判断を求めるとき

#### `CWK-IMPL-014` 推測で実装しない

Implementer は、次のどれかに当たったら、推測で進めずに `blocked` を返す。cumin は、やり直さずに止める (`CWK-IMPL-008`)。

##### `CWK-IMPL-014.1` 足りない要件

実装に要る要件が足りないか、要件どうしが食い違っているときは、止まる。理由には、何が決まっていないかを書く。

- 検証: `demo` 要件の足りない実装Issueで実装すると、結果は `blocked` で、何が決まっていないかが理由に書いてある

---

##### `CWK-IMPL-014.2` 変えられないファイル

保護されたパスか `.github/workflows` の変更が要るときは、変えずに止まる。理由には、そのパスを書く。

- 検証: `demo` 保護されたパスの変更が要る実装Issueで実装すると、そのパスは変わらず、結果は `blocked` で、理由にそのパスが書いてある

---

##### `CWK-IMPL-014.3` 大きすぎる実装Issue

実装Issueが、1つの Pull Request に収まらない大きさだと分かったときは、止まる。

- 検証: `demo` 1つの Pull Request に収まらない実装Issueで実装すると、結果は `blocked` で、分け方が理由に書いてある

---

##### `CWK-IMPL-014.4` 足りない先の作業

blocked by の Issue で終わっているはずの作業が、実際には足りていないときは、止まる。`CWK-IMPL-015` で始めた実装Issueでは、blocked by の Issue のブランチにあるはずの作業である。

- 検証: `demo` 先の実装Issueの成果が main ブランチにない状態で実装すると、結果は `blocked` で、足りない作業が理由に書いてある

---

## 受け入れテスト

| # | 場面 | 期待する結果 |
|---|---|---|
| 1 | Maintainer が実装Issueに `cumin/status/ready` を付ける。Implementer が Pull Request を開き、必須チェックが完了する | 実装が始まり (`CWK-IMPL-001`)、実装が完了したと判断されて `cumin/status/checking` になり (`CWK-IMPL-005`、`CWK-IMPL-006`)、必須チェックが完了するとレビューに進む ([チェック](../check/index.md) の `CWK-CHECK-002`) |
| 2 | 保護されたパスの変更が要る実装Issueで実装する | Implementer が `blocked` を返し (`CWK-IMPL-014.2`)、やり直さずに `cumin/status/awaiting-decision` になる (`CWK-IMPL-008`) |
| 3 | Maintainer が止まった実装Issueを直し、`cumin/status/ready` を付け直す | 新しいセッションで続きから進み、新しい Pull Request は作られない (`CWK-IMPL-001`、`CWK-IMPL-009`)。チェックの修正の回数は0から数え直す (`CWK-IMPL-004`) |
| 4 | 先の実装Issueが Assignee のマージ判断を待っている間に、それに依存する実装Issueに `cumin/status/ready` が付いている | blocked by の Issue のブランチから切ったブランチで実装が始まり、Pull Request の base がそのブランチになる (`CWK-IMPL-015.1`、`CWK-IMPL-015.2`)。そのあとは [マージ](../merge/index.md) の、blocked by の Issue のマージを待つ流れに進む |

## 未決事項

なし
