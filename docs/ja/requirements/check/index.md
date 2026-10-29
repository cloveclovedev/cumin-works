# チェック

実装IssueのPull Requestで、必須チェックが完了するまでの流れを決める。cuminが必須チェックの結果を待ち、失敗と衝突をImplementerに直させ、完了したらレビューに進めるまでである。cuminが対象のリポジトリに置く、保護されたパスのチェックもここで決める。Implementerの作業は[実装](../impl/index.md)、レビューは[レビュー](../review/index.md)で決める。

## 全体像

実装Issueがチェックでたどる状態を示す。灰色の状態は、ほかのカテゴリで決める。

![チェックの全体像](overview.svg)

図の元ファイル: [overview.puml](overview.puml)

`cumin/status/checking`は、Implementerの作業が済み、チェックが動いていることを表す。`cumin/status/implementing`のままでは、修正の途中かチェックを待っているかをGitHubから見分けられないためである。この間はAgentが動かず、利用制限を使わない。

## 要件

### 必須チェック

#### `CWK-CHECK-001` 必須チェックの一覧

必須チェックは、mainブランチに効くrulesetの "Require status checks to pass before merging" に登録されたチェックである。cuminは、その一覧をrulesetから読む。コミットに付いているチェックからは決めない。pushの直後は、チェックがないのか、これから現れるのかを見分けられないためである。

- 検証: `test` rulesetに登録したチェックだけを待ち、登録していないチェックの失敗では止まらない

---

#### `CWK-CHECK-002` 必須チェックの完了を判断する

cuminは、一覧のすべてのチェックが、先頭のコミットで`success`、`skipped`、`neutral`のどれかを返したとき、必須チェックが完了したと判断する。GitHubも、この3つでマージを止めない。一覧が空なら、すぐに完了と判断する。

- 検証: `test` 3つの結論は完了と判断し、ほかの結論は判断しない。一覧が空のリポジトリでは、次の定期確認でレビューに進む

---

#### `CWK-CHECK-003` 判定の順番

`cumin/status/checking`の実装Issueでは、1回の定期確認で、衝突の解消（`CWK-CHECK-007`）、チェックの結果（レビューに進む、`CWK-CHECK-004`、`CWK-CHECK-005`）、`CWK-CHECK-006`の順に判定し、最初に成り立ったものだけを行う。

- 検証: `test` 衝突していて、待ち時間も過ぎた実装Issueでは、衝突の解消だけが起きる

---

#### `CWK-CHECK-004` チェックの失敗を修正する

`cumin/status/checking`の実装Issueで、必須チェックのどれかが失敗し、チェックの修正を頼んだ回数が上限（`max_check_fix_requests`、初期値は3回）に達していないとき、cuminは状態を`cumin/status/implementing`に替えて、Implementerの直前のセッションで、失敗したチェックの内容を添えて修正を頼む。

- 検証: `test` 必須チェックが失敗すると、直前のセッションでImplementerが1回起動され、失敗の内容が渡される

---

#### `CWK-CHECK-005` チェックの失敗で止める

`cumin/status/checking`の実装Issueで、必須チェックのどれかが失敗し、修正を頼んだ回数が上限に達しているとき、cuminは状態を`cumin/status/awaiting-decision`に替えて、通知する。コメントと通知には、失敗したチェックの名前と、頼んだ回数を書く。

- 検証: `test` 上限まで頼んでも失敗すると、`cumin/status/awaiting-decision`になり、通知は1回だけである

---

#### `CWK-CHECK-006` チェックが来ないときに止める

`cumin/status/checking`の実装Issueで、待ち時間（`checks_wait_time`、初期値は60分）を過ぎても、必須チェックのどれかが先頭のコミットで結果を返していないとき、cuminは状態を`cumin/status/awaiting-decision`に替えて、通知する。

- 待ち時間は、最新の`cumin/status/checking`が付いた時刻と、先頭のコミットの時刻の、遅いほうから数える
- 開いているPull Requestがないときも、ラベルの時刻から待ち時間を過ぎたら止める
- コメントと通知には、先頭のコミット、結果を返していない必須チェック、待った時間を書く。Pull Requestがないときは、そのことを書く。理由はAssigneeが調べる。

- 検証: `test` 待ち時間を過ぎると、先頭のコミット、そのチェック、待った時間を添えて、通知が1回だけ届く
- 検証: `test` 開いているPull Requestがなくなった実装Issueでも、待ち時間を過ぎると止まる

---

### 衝突の解消

#### `CWK-CHECK-007` 衝突の解消

`cumin/status/checking`か`cumin/status/awaiting-merge-decision`の実装Issueで、GitHubがPull Requestとそのbaseの衝突を返したとき（`mergeable`が`CONFLICTING`）、cuminは状態を`cumin/status/implementing`に替えて、Implementerの直前のセッションで衝突の解消を頼む。baseは、ふつうはmainブランチで、`CWK-IMPL-015`で始めたPull Requestではblocked byのIssueのブランチである。`cumin/status/merging`からの衝突の解消は[マージ](../merge/index.md)で決める。

- `mergeable`が`UNKNOWN`の間は、GitHubが計算中なので判定しない。`UNKNOWN`のまま待ち時間を過ぎたら、`CWK-CHECK-006`で止まる。
- 衝突の解消は、チェックの修正の回数に含めない。衝突はほかのPull Requestのマージで起き、Implementerの誤りではないからである。
- 衝突のあるPull Requestでは、GitHubが`pull_request`のワークフローを動かさないので、待たずに解消させる

- 検証: `test` 衝突すると、衝突の解消を1回だけ頼み、チェックの修正の回数は増えない
- 検証: `test` `mergeable`が`UNKNOWN`の間は、衝突の解消を頼まない

---

### 保護されたパスのチェック

Agentは保護されたパスのファイルを変えない（`CWK-AGENT-021`）。それを守らせるのが、このチェックである。

#### `CWK-CHECK-008` 保護されたパスのチェック

保護されたパスが変わっていないことは、cuminではなく、GitHub Actionsのチェックで確かめる。失敗したときは、ほかの必須チェックと同じく`CWK-CHECK-004`に進む。

##### `CWK-CHECK-008.1` 保護されたパスの一覧

保護されたパスの一覧は、対象のリポジトリの`.cumin/config.toml`の`protected_paths`に書く。ファイルかキーがなければ、初期値の`.cumin/`、`CLAUDE.md`、`AGENTS.md`、`.claude/`、`REVIEW.md`を使う。書いた一覧は、初期値に足すのではなく、初期値を置き換える。`REVIEW.md`を含めるのは、Agentが書き換えると、Reviewerの判断が変わるためである（`CWK-REVIEW-017`）。

照合の決まりは、`.gitignore`の一部と同じである。

- 途中に`/`を含まない項目（`CLAUDE.md`、`.claude/`）は、どの階層にあっても当たる。サブディレクトリの`CLAUDE.md`もAgentへの指示になるためである
- 先頭か途中に`/`を含む項目は、リポジトリの直下からの位置にだけ当たる
- 末尾が`/`の項目は、そのディレクトリの下のすべてに当たる
- ワイルドカードは使えない
- 大文字と小文字を区別しない

- 検証: `test` `protected_paths`のないリポジトリで、`REVIEW.md`を変えるbotのPull Requestのチェックが失敗する

---

##### `CWK-CHECK-008.2` 変更ファイルと突き合わせる

対象のリポジトリに置くワークフローが、Pull Requestの変更ファイルを保護されたパスの一覧と突き合わせ、1つでも当たれば失敗する。

- 検証: `demo` 保護されたパスを変えるPull Requestをbotが開くと、チェックが失敗する

---

##### `CWK-CHECK-008.3` 必須チェックにする

このチェックを、mainブランチのrulesetで必須チェックに登録する。

- 検証: `inspection` 対象のリポジトリのrulesetに、このチェックが必須チェックとして登録されている

---

##### `CWK-CHECK-008.4` botのPull Requestだけに掛ける

チェックのjobは、Pull Requestの作成者がbotのときだけ動く。人のPull Requestではjobが飛ばされて`skipped`になり、止まらない。条件にGitHub Appの名前は使わない。名前を間違えると、jobが飛ばされて保護が黙って効かなくなるためである。

- 検証: `demo` 人が保護されたパスを変えるPull Requestを開くと、チェックが`skipped`になる
- 検証: `inspection` ワークフローの条件が、GitHub Appの名前ではなく、作成者がbotかどうかで決まっている

---

##### `CWK-CHECK-008.5` mainブランチの一覧を読む

ワークフローは、保護されたパスの一覧を、Pull Requestのブランチではなくmainブランチから読む。一覧のある`.cumin/`も、保護されたパスに含める。Pull Requestのブランチから読むと、同じPull Requestで一覧から自分の変更を外せば、チェックが成功してしまうためである。`.cumin/`を含めるのは、一覧を弱めるPull Requestがマージされないためである。

- 検証: `demo` Pull Requestのブランチで一覧から自分の変更を外しても、チェックが失敗する

---

## 受け入れテスト

| # | 場面 | 期待する結果 |
|---|---|---|
| 1 | 必須チェックが失敗し、Implementerが直す | 直前のセッションで修正を頼み（`CWK-CHECK-004`）、同じPull Requestに修正が足され（`CWK-IMPL-009`）、`cumin/status/checking`に戻って必須チェックが完了する |
| 2 | `cumin/status/checking`の間に、ほかのPull Requestのマージで衝突する | `UNKNOWN`の間は待ち、`CONFLICTING`になると衝突の解消を1回だけ頼む（`CWK-CHECK-007`）|
| 3 | 必須チェックが待ち時間を過ぎても結果を返さない | `cumin/status/awaiting-decision`になり、通知が1回届く（`CWK-CHECK-006`）|

## 未決事項

なし
