# 受け入れ

要求Issueの sub-issue がすべて閉じてから、要求Issueが閉じるまでの流れを決める。Planner の受け入れ確認、残りの項目の仕分け、Assignee の受け入れと差し戻しを扱う。sub-issue に `cumin/status/ready` を付けたときの要求Issueの状態は [計画](../plan/index.md)、フォローアップノートは [マージ](../merge/index.md) で決める。

## 全体像

要求Issueが受け入れでたどる状態を示す。灰色の状態は、ほかのカテゴリで決める。

![受け入れの全体像](overview.svg)

図の元ファイル: [overview.puml](overview.puml)

「Planner が動いていない」の意味は、[Agent](../agent/index.md) の `CWK-AGENT-001` で決める。

受け入れ確認では、Planner が、すべての sub-issue を合わせた main ブランチで、要求Issueの要求を1項目ずつ確かめる。Pull Request は1つずつしか確かめられていないためである。Planner は、残りの項目も仕分け、この要求で直すものを sub-issue にする。

cumin が読むのは、受け入れ確認のコメントがあるか、sub-issue が開いているか、開いている sub-issue のリスクのラベルだけである。コメントの中身は読まない。

## 要件

### 受け入れ確認の Agent起動

#### `CWK-ACCEPT-001` 受け入れ確認を始める

要求Issueが次のすべてを満たすとき、cumin は状態を `cumin/status/accepting` に替えてから、新しいセッションで Planner に受け入れ確認を頼む。

- 状態が `cumin/status/implementing` か `cumin/status/awaiting-plan-review` である。または `cumin/status/planning` で、Planner が動いておらず、計画が完了している ([計画](../plan/index.md) の `CWK-PLAN-004`)
- その状態ラベルを cumin が扱う ([権限](../access/index.md) の `CWK-ACCESS-004`)
- sub-issue が1つ以上あり、すべて閉じている
- 最後の sub-issue が閉じたあとの受け入れ確認のコメント (`CWK-ACCEPT-002`) がない
- フォローアップノートを書き終えている ([マージ](../merge/index.md) の `CWK-MERGE-018`)
- そのリポジトリで同時に進める Issue の数に余裕がある ([定期確認](../poll/index.md) の `CWK-POLL-005`)

- 検証: `test` 最後の sub-issue の Pull Request がマージされると、`cumin/status/accepting` になり、新しいセッションで Agent起動が1回起きる
- 検証: `test` Assignee が最後の sub-issue を閉じた `cumin/status/awaiting-plan-review` の要求Issueでも、受け入れ確認が始まる
- 検証: `test` 同時に進める Issue の数に余裕がない間は始めず、余裕ができた定期確認で始める

---

### 受け入れ確認の完了判断

#### `CWK-ACCEPT-002` 受け入れ確認の完了を判断する

cumin は、`cumin-planner` が要求Issueに書いた `## Acceptance check` で始まるコメントが、最後の sub-issue が閉じたあとにあるとき、受け入れ確認が完了したと判断する。それより前のコメントは使わない。Planner が返した結果も使わない。

- 検証: `test` 最後の sub-issue が閉じる前のコメントしかないと、完了したと判断しない

---

#### `CWK-ACCEPT-003` 受け入れを求める

`cumin/status/accepting` の要求Issueで、Planner が動いておらず、受け入れ確認が完了し、sub-issue がすべて閉じているとき、cumin は状態を `cumin/status/awaiting-acceptance` に替えて、通知する。`Fail` があっても同じである。

- 検証: `test` 修正の sub-issue のない受け入れ確認のあとで、`cumin/status/awaiting-acceptance` になり、通知が1回届く

---

#### `CWK-ACCEPT-004` 修正の sub-issue の確認を求める

`cumin/status/accepting` の要求Issueで、Planner が動いておらず、受け入れ確認が完了し、開いている sub-issue があり、そのすべてにリスクのラベルが1つだけ付いているとき、cumin は状態を `cumin/status/awaiting-plan-review` に替えて、通知する。そのあとは、計画の sub-issue と同じに進む ([計画](../plan/index.md) の `CWK-PLAN-008`)。

- 検証: `test` Planner が修正の sub-issue を作ると、`cumin/status/awaiting-plan-review` になり、通知が1回届く
- 検証: `test` 修正の sub-issue がすべて閉じると、受け入れ確認がもう一度始まる

---

#### `CWK-ACCEPT-005` 受け入れ確認をやり直す

`cumin/status/accepting` の要求Issueで、Planner が動いておらず、質問のコメントがなく、次のどちらかが成り立つとき、cumin は Planner をもう一度起動する。やり直すのは、1つの `cumin/status/accepting` の間に1回だけである。

- 受け入れ確認のコメントがない
- コメントはあるが、リスクのラベルが1つだけでない、開いている sub-issue がある

- 検証: `test` コメントを書かずに終わった Planner のあとで、Agent起動がもう一度だけ起きる
- 検証: `test` リスクのラベルのない修正の sub-issue が残ると、Agent起動がもう一度だけ起きる

---

#### `CWK-ACCEPT-006` 受け入れ確認を止める

`cumin/status/accepting` の要求Issueで、Planner が動いておらず、次のどちらかが成り立つとき、cumin は状態を `cumin/status/awaiting-decision` に替えて、通知する。質問のコメントでないときは、理由をコメントに書く。

- Planner の質問のコメントが、`cumin/status/accepting` になったあとにある
- やり直しのあとも、`CWK-ACCEPT-005` の条件が成り立つ

Assignee が答えを書いて要求Issueに `cumin/status/ready` を付けると、Planner が要求Issueを読み直す ([計画](../plan/index.md) の `CWK-PLAN-001`)。

- 検証: `test` Planner が `blocked` を返すと、やり直さずに止まる
- 検証: `test` やり直しのあともコメントがないと、理由のコメントが付いて止まる

---

### Assignee の受け入れと差し戻し

#### `CWK-ACCEPT-007` 受け入れ

Assignee は、要求Issueを閉じて受け入れる。判断が要る項目 (`CWK-ACCEPT-014`) は、Assignee が新しい要求Issueかバックログに書く。cumin は、閉じた要求Issueに何もしない。

- 検証: `test` 要求Issueを閉じたあと、その要求Issueにアクションが起きない

---

#### `CWK-ACCEPT-008` 差し戻し

Assignee は、要求Issueに sub-issue を足し、足した sub-issue に `cumin/status/ready` を付けて差し戻す。足した sub-issue がすべて閉じると、受け入れ確認がもう一度始まる。

- 検証: `test` `cumin/status/awaiting-acceptance` の要求Issueに sub-issue を足し、その sub-issue に `cumin/status/ready` を付けると、`cumin/status/implementing` になり、その sub-issue が閉じると受け入れ確認がもう一度始まる

---

### Planner の成果物

#### `CWK-ACCEPT-009` 受け入れ確認のコメント

Planner は、要求Issueに受け入れ確認のコメントを1つ書く。形式は [acceptance-check.md](../../../../templates/acceptance-check.md) に従う。main ブランチの最新の内容で、要求と制約を1項目ずつ確かめ、項目ごとに結果 (`Pass` か `Fail`)、証拠、対応した Pull Request を書く。証拠は、実行したコマンドと結果、または読んだファイルと行である。

- 検証: `demo` 受け入れ確認を頼むと、決められた形式のコメントが1つ付き、要求の項目ごとに結果と証拠がある

---

#### `CWK-ACCEPT-010` Planner が読むもの

Planner は、sub-issue を閉じた Pull Request とその説明、フォローアップノート、自分の計画の全体像のコメント ([計画](../plan/index.md) の `CWK-PLAN-017`) を読む。計画の全体像で「含めない」とした項目は、受け入れ確認のコメントにも書く。

- 検証: `demo` 計画の全体像の「含めない」項目が、受け入れ確認のコメントにもある

---

#### `CWK-ACCEPT-011` 残りの項目を仕分ける

Planner は、残りの項目のすべてを、次の3つのどれか1つに入れ、1行の理由を付ける。残りの項目とは、`Fail` の行、フォローアップノートの項目、`Follow-up` 以外に書かれた範囲の外の作業である。

- この要求で直す: 要求の範囲の中の不具合、小さな修正、文書とコードのずれ
- 判断が要る: 要求の範囲を変えるもの、選択が要るもの
- 対応しない: 作業が要らないもの

##### `CWK-ACCEPT-011.1` 仕分けの順

Planner は、次の順に問い、最初に当てはまった組に入れる。迷ったら「判断が要る」に入れる。

1. 作業が要らないか (済んだもの、重なるもの、設計を説明するだけのもの)。当てはまれば「対応しない」
2. 範囲を変えるか、選択が要るか (要求にない振る舞い、案が2つ以上、要件の文書の変更)。当てはまれば「判断が要る」
3. それ以外は「この要求で直す」

- 検証: `demo` 済んだ項目、新しい設定の提案、文書とコードのずれが、それぞれ「対応しない」「判断が要る」「この要求で直す」に入り、理由がある

---

#### `CWK-ACCEPT-012` 修正の sub-issue

Planner は、「この要求で直す」の項目ごとに、要求Issueの sub-issue を1つ作る。計画の実装Issueと同じ形式で書き ([計画](../plan/index.md) の `CWK-PLAN-010`)、リスクのラベルを1つ付け、状態ラベルは付けない。本文には元の項目へのリンクを書く。sub-issue をすべて作ってから、コメントを書く。コメントには、作った sub-issue の番号を書く。

- 検証: `demo` 文書とコードのずれが1つあると、元の項目へのリンクとリスクのラベルのある sub-issue が1つでき、コメントにその番号がある

---

#### `CWK-ACCEPT-013` 修正の sub-issue は1回だけ作る

修正の sub-issue を作るのは、1つの要求Issueで最初の受け入れ確認のときだけである。受け入れ確認と修正が止まらずに往復しないためである。

Planner は、最後の sub-issue が閉じる前に `cumin-planner` が書いた受け入れ確認のコメントがあれば、2回目以降だと判断する。2回目以降は、「この要求で直す」に当たる項目も、理由を付けて「判断が要る」に入れる。

- 検証: `demo` 修正の sub-issue が閉じたあとの受け入れ確認では、sub-issue を作らず、その項目が「判断が要る」に入る

---

#### `CWK-ACCEPT-014` 判断が要る項目

Planner は、「判断が要る」の項目ごとに、行き先 (新しい要求Issueか、バックログ) と、その文の下書きを書く。要求Issueもバックログも、Planner は書かない。

- 検証: `demo` 範囲を変える提案に、行き先と下書きがあり、Issue とバックログは変わっていない

---

#### `CWK-ACCEPT-015` 直さない

Planner は、受け入れ確認でコードを直さず、要求Issueの本文とラベルを変えない。作ってよい Issue は、修正の sub-issue だけである。

- 検証: `demo` 受け入れ確認のあとで、コードと要求Issueは変わらず、増えた Issue は修正の sub-issue だけである

---

#### `CWK-ACCEPT-016` やり直しで二重に作らない

やり直しの Planner は、同じ元の項目へのリンクを持つ sub-issue を作らず、足りない分だけを作る。リスクのラベルが1つだけでない sub-issue は、ラベルを直す。

受け入れ確認のコメントは、最後の sub-issue が閉じたあとのものだけを書き直す。それより古いコメントしかなければ、新しく書く。古いコメントを書き直しても、完了と判断されないためである (`CWK-ACCEPT-002`)。

- 検証: `demo` sub-issue を1つ作ったところで止まった受け入れ確認をやり直すと、足りない sub-issue とコメントだけが足される
- 検証: `demo` 前の受け入れ確認のコメントがあると、それは変わらず、新しいコメントが1つ付く

---

## 受け入れテスト

| # | 場面 | 期待する結果 |
|---|---|---|
| 1 | 最後の sub-issue がマージされ、要求がすべて満たされ、直すものがない | フォローアップノートのあとに受け入れ確認が始まり (`CWK-ACCEPT-001`)、仕分けのあるコメントが付いて (`CWK-ACCEPT-009`、`CWK-ACCEPT-011`)、受け入れを求める通知が届く (`CWK-ACCEPT-003`)。閉じると何も起きない (`CWK-ACCEPT-007`) |
| 2 | 受け入れ確認で、文書とコードのずれと、範囲を変える提案が見つかる | ずれは sub-issue になり (`CWK-ACCEPT-012`)、提案には行き先と下書きが付く (`CWK-ACCEPT-014`)。計画結果の確認を経て sub-issue が閉じると、受け入れ確認がもう一度行われ、そこでは sub-issue を作らない (`CWK-ACCEPT-004`、`CWK-ACCEPT-013`) |
| 3 | Assignee が sub-issue を足して差し戻す | `cumin/status/implementing` になり (`CWK-ACCEPT-008`)、足した sub-issue が閉じると、新しいコメントで受け入れ確認が行われる (`CWK-ACCEPT-016`) |
| 4 | Planner が質問して止まり、Assignee が答えて `cumin/status/ready` を付ける | 止まって通知が届き (`CWK-ACCEPT-006`)、Planner が読み直して、作るものがなければ受け入れ確認に戻る (`CWK-ACCEPT-001`) |
| 5 | Assignee が手で行う作業の sub-issue を、最後に閉じる | `cumin/status/awaiting-plan-review` から受け入れ確認が始まる (`CWK-ACCEPT-001`) |

## 未決事項

なし
