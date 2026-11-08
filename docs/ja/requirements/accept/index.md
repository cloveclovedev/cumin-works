# 受け入れ

要求Issueの実装Issueがすべて閉じてから、要求Issueが閉じるまでの流れを決める。Plannerの受け入れ確認、残課題の仕分け、Assigneeの受け入れと差し戻しを扱う。実装Issueに`cumin/status/ready`を付けたときの要求Issueの状態は[計画](../plan/index.md)、フォローアップノートは[マージ](../merge/index.md)で決める。

## 全体像

要求Issueが受け入れでたどる状態を示す。灰色の状態は、ほかのカテゴリで決める。

![受け入れの全体像](overview.svg)

図の元ファイル: [overview.puml](overview.puml)

受け入れ確認では、Plannerが、すべての実装Issueを合わせたmainブランチで、要求Issueの要求を1項目ずつ確かめる。Pull Requestは1つずつしか確かめられていないためである。Plannerは、残課題も仕分け、この要求で直すものを実装Issueにする。

cuminが読むのは、受け入れ確認のコメントと質問のコメントがあるか、フォローアップノートがあるか、実装Issueが開いているか、開いている実装Issueのリスクのラベルだけである。コメントの中身は読まない。

## 要件

### 受け入れ確認のAgent起動

#### `CWK-ACCEPT-001` 受け入れ確認を始める

要求Issueが次のすべてを満たすとき、cuminは状態を`cumin/status/accepting`に替えてから、新しいセッションでPlannerに受け入れ確認を頼む。

- 状態が`cumin/status/implementing`か`cumin/status/awaiting-plan-review`である。または`cumin/status/planning`で、計画が完了している（[計画](../plan/index.md)の`CWK-PLAN-003`）
- その状態ラベルを、`cumin-core`のGitHub Appが付けている（[権限](../access/index.md)の`CWK-ACCESS-012`）
- 実装Issueが1つ以上あり、すべて閉じている
- 最後の実装Issueが閉じたあとの受け入れ確認のコメント（`CWK-ACCEPT-002`）がない
- `cumin/status/ready`が付いていない
- 閉じた実装Issueを閉じるマージ済みのPull Requestのすべてに、フォローアップノートがある（[マージ](../merge/index.md)の`CWK-MERGE-016`）

- 検証: `test` GitHub mockとCLI mockでテスト

---

### 受け入れ確認の完了判断

#### `CWK-ACCEPT-002` 受け入れ確認の完了を判断する

cuminは、次のすべてが成り立つとき、受け入れ確認が完了したと判断する。

- Plannerの実行が終わっている
- `cumin-planner`が要求Issueに書いた`## Acceptance check`で始まるコメントが、最後の実装Issueが閉じたあとにある

それより前のコメントは使わない。Plannerが返した結果も使わない。

- 検証: `test` GitHub mockとCLI mockでテスト

---

#### `CWK-ACCEPT-003` 受け入れを求める

`cumin/status/accepting`の要求Issueで、受け入れ確認が完了し（`CWK-ACCEPT-002`）、実装Issueがすべて閉じているとき、cuminは状態を`cumin/status/awaiting-acceptance`に替えて、通知する。

- 検証: `test` GitHub mockとCLI mockでテスト

---

#### `CWK-ACCEPT-004` 修正の実装Issueの確認を求める

`cumin/status/accepting`の要求Issueで、受け入れ確認が完了し（`CWK-ACCEPT-002`）、開いている実装Issueがあり、そのすべてにリスクのラベルが1つだけ付いているとき、cuminは状態を`cumin/status/awaiting-plan-review`に替えて、通知する。そのあとは、計画の実装Issueと同じに進む（[計画](../plan/index.md)の`CWK-PLAN-007`）。

- 検証: `test` GitHub mockとCLI mockでテスト

---

#### `CWK-ACCEPT-005` 受け入れ確認をやり直す

`cumin/status/accepting`の要求Issueで、Plannerの実行が終わっていて、`cumin/status/accepting`になったあとにPlannerの質問のコメントがなく、次のどちらかが成り立つとき、cuminはPlannerをもう一度起動する。やり直すのは、1つの`cumin/status/accepting`の間に1回だけである。

- 受け入れ確認が完了していない（`CWK-ACCEPT-002`）
- 受け入れ確認は完了したが、リスクのラベルが1つだけでない、開いている実装Issueがある

- 検証: `test` GitHub mockとCLI mockでテスト

---

#### `CWK-ACCEPT-006` 受け入れ確認を止める

`cumin/status/accepting`の要求Issueで、Plannerの実行が終わっていて、次のどちらかが成り立つとき、cuminは状態を`cumin/status/awaiting-decision`に替えて、通知する。

- Plannerの質問のコメントが、`cumin/status/accepting`になったあとにある
- やり直しのあとも、`CWK-ACCEPT-005`の条件が成り立つ

Assigneeが答えを書いて要求Issueに`cumin/status/ready`を付けると、Plannerが要求Issueを読み直す（[計画](../plan/index.md)の`CWK-PLAN-001`）。

- 検証: `test` GitHub mockとCLI mockでテスト

---

### Assigneeの受け入れと差し戻し

#### `CWK-ACCEPT-007` 受け入れ

Assigneeは、要求Issueを閉じて受け入れる。判断が必要な残課題（`CWK-ACCEPT-014`）は、Assigneeが新しい要求Issueかバックログに書く。閉じた要求Issueには、cuminは何もしない（[定期確認](../poll/index.md)の`CWK-POLL-001`）。

- 検証: `test` 閉じた要求Issueに何もしないことを、GitHub mockでテスト
- 検証: `inspection` 受け入れと、判断が必要な残課題の扱いが、受け入れ確認のテンプレートに書かれている

---

#### `CWK-ACCEPT-008` 差し戻し

Assigneeは、要求Issueに実装Issueを足し、足した実装Issueに`cumin/status/ready`を付けて差し戻す。cuminは要求Issueを`cumin/status/implementing`に替え（[計画](../plan/index.md)の`CWK-PLAN-007`）、足した実装Issueがすべて閉じると、受け入れ確認をもう一度始める（`CWK-ACCEPT-001`）。足した実装Issueが残課題に対応するときは、本文にそのIDを書く。

- 検証: `test` GitHub mockでテスト

---

### Plannerの成果物

#### `CWK-ACCEPT-009` 受け入れ確認のコメント

Plannerは、要求Issueに受け入れ確認のコメントを1つ書く。形式は[acceptance-check.md](../../../../templates/acceptance-check.md)に従う。mainブランチの最新の内容で、要求と制約を1項目ずつ確かめ、項目ごとに次のことを書く。

- ID。要求Issueの「Requirements」のn行目は`R<n>`、「Constraints」のn行目は`C<n>`とする。その行が要件のIDを書いていれば、それも書く
- 結果（`Pass`か`Fail`）
- 証拠。実行したコマンドと結果、または読んだファイルと行
- 対応したPull Request

- 検証: `demo` テスト用の実リポジトリでPlannerを動かして確認

---

#### `CWK-ACCEPT-010` Plannerが読むもの

Plannerは、実装Issueを閉じたPull Requestとその説明、フォローアップノート、自分の計画の全体像のコメント（[計画](../plan/index.md)の`CWK-PLAN-016`）を読む。計画の全体像で「含めない」とした項目は、受け入れ確認のコメントにも書く。

- 検証: `demo` テスト用の実リポジトリでPlannerを動かして確認

---

#### `CWK-ACCEPT-011` 残課題を仕分ける

Plannerは、残課題のすべてを、次の3つのどれか1つに入れ、1行の理由を付ける。残課題とは、`Fail`の行と、フォローアップノートの項目である。どの残課題も、IDで示す（`CWK-ACCEPT-009`、[マージ](../merge/index.md)の`CWK-MERGE-017`）。

- この要求で直す: 要求の範囲の中の不具合、小さな修正、文書とコードのずれ
- 判断が必要: 要求の範囲を変えるもの、選択が要るもの
- 対応しない: 作業が要らないもの

##### `CWK-ACCEPT-011.1` 仕分けの順

Plannerは、次の順に問い、最初に当てはまった組に入れる。迷ったら「判断が必要」に入れる。

1. 作業が要らないか（済んだもの、重なるもの、設計を説明するだけのもの）。当てはまれば「対応しない」
2. 範囲を変えるか、選択が要るか（要求にない振る舞い、案が2つ以上、要件の文書の変更）。当てはまれば「判断が必要」
3. それ以外は「この要求で直す」

- 検証: `demo` テスト用の実リポジトリでPlannerを動かして確認

---

#### `CWK-ACCEPT-012` 修正の実装Issue

Plannerは、この要求で直す残課題ごとに、要求Issueの実装Issueを1つ作る。計画の実装Issueと同じ形式で書き（[計画](../plan/index.md)の`CWK-PLAN-009`）、リスクのラベルを1つ付け、状態ラベルは付けない。本文には元の残課題のIDを書く。実装Issueをすべて作ってから、コメントを書く。コメントには、作った実装Issueの番号を書く。

- 検証: `demo` テスト用の実リポジトリでPlannerを動かして確認

---

#### `CWK-ACCEPT-013` 修正の実装Issueは1回だけ作る

修正の実装Issueを作るのは、1つの要求Issueで最初の受け入れ確認のときだけである。受け入れ確認と修正が止まらずに往復しないためである。

Plannerは、最後の実装Issueが閉じる前に`cumin-planner`が書いた受け入れ確認のコメントがあれば、2回目以降だと判断する。2回目以降は、この要求で直す残課題も、理由を付けて「判断が必要」に入れる。

- 検証: `demo` テスト用の実リポジトリでPlannerを動かして確認

---

#### `CWK-ACCEPT-014` 判断が必要な残課題

Plannerは、受け入れ確認のコメントに、判断が必要な残課題ごとに、勧める行き先（新しい要求Issueか、バックログ）を一言で書く。要求Issueもバックログも、Plannerは書かない。

- 検証: `demo` テスト用の実リポジトリでPlannerを動かして確認

---

#### `CWK-ACCEPT-015` 直さない

Plannerは、受け入れ確認でコードを直さず、要求Issueの本文とラベルを変えない。作ってよいIssueは、修正の実装Issueだけである。

- 検証: `demo` テスト用の実リポジトリでPlannerを動かして確認

---

#### `CWK-ACCEPT-016` やり直しで二重に作らない

やり直しのPlannerは、前の実行の続きから進める。

- 修正の実装Issueは、同じIDを書いたものがあれば作らず、足りない分だけを作る。リスクのラベルが1つだけでないものは、ラベルを直す
- 受け入れ確認のコメントは、最後の実装Issueが閉じたあとのものがあれば書き直し、なければ新しく書く。それより前のコメントを書き直しても、完了と判断されない（`CWK-ACCEPT-002`）。

- 検証: `demo` テスト用の実リポジトリでPlannerを動かして確認

---

## 未決事項

なし
