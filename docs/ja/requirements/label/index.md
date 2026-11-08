# ラベル

IssueとPull Requestに付けるラベルを決める。Maintainerはラベルで状態を知り、ラベルでcuminに合図する。状態ごとのアクションは各カテゴリで決める。誰がどのラベルを付けてよいかは[権限](../access/index.md)で決める。

## 全体像

ラベルには以下の種類がある。

- `cumin/type/*`: Issueの種類。状態ではないので、閉じるまで付けたままにする。
- `cumin/status/*`: Issueの状態
- `cumin/risk/*`: 実装Issueのマージのリスク
- `cumin/priority/*`: 着手の順番（[定期確認](../poll/index.md)）

状態の命名規則は以下に定める。

- 人なしで進む状態: 名前が`-ing`で終わる。Agent、GitHub、cuminのどれかが作業している。
- cuminの外を待つ状態: 名前が`awaiting-`で始まる。Assigneeの判断や、ほかのIssueのマージのように、cuminが自分では進められないことを待つ。
- 順番を待つ状態: `ready`。Maintainerが進めてよいと合図し、同時に進めるIssueの数に余裕ができるのを、cuminが待っている。

状態がどう移るかの全体は、[要求と要件の入口](../index.md)の全体像の図にある。

## 要件

### 種類のラベル

#### `CWK-LABEL-001` 種類のラベル

Issueの種類は、次のどれかである。

| 種類 | ラベル | 付ける人 |
|---|---|---|
| 要求Issue | `cumin/type/requirement` | Maintainer |
| 実装Issue | なし | なし |
| Assigneeが手で行う実装Issue | `cumin/type/owner-task` | PlannerかMaintainer |

`cumin/type/requirement`は、そのIssueが要求Issueであることを表す。Maintainerが付け、閉じるまで付けたままにする。`cumin/type/requirement`の付いたIssueのsub-issueが、実装Issueである。実装Issueを表すラベルは作らない。

`cumin/type/owner-task`は、その実装Issueの作業をAssigneeが手で行うことを表す。Implementerが変えられないファイル（保護されたパス、GitHub Appの権限で書けないファイル）の変更が要るときに、PlannerかMaintainerが付ける。cuminは、このIssueに着手しない。

- 検証: `inspection` ラベルの名前がコードの定義と一致すること

---

### 状態のラベル

#### `CWK-LABEL-002` 状態の一覧

Issueの状態は、次のどれかである。状態ラベルの名前は、`cumin/status/`のあとに状態の名前を付けたものである。

| 状態 | 付く対象 | その状態で期待されるアクション |
|---|---|---|
| `ready` | 要求Issue、実装Issue | 同時に進めるIssueの数に余裕ができるのを、cuminが待つ |
| `planning` | 要求Issue | Plannerが計画する |
| `awaiting-plan-review` | 要求Issue | Assigneeが計画結果を確かめる |
| `implementing` | 要求Issue | Implementerが要求Issueに紐づく実装Issueを実装する |
| `accepting` | 要求Issue | Plannerが受け入れ確認をする |
| `awaiting-acceptance` | 要求Issue | Assigneeが、受け入れるか差し戻すかを決める |
| `implementing` | 実装Issue | Implementerが作業する（修正を含む）|
| `checking` | 実装Issue | GitHubが必須チェックを動かす |
| `reviewing` | 実装Issue | ReviewerがPull Requestをレビューする |
| `awaiting-merge-decision` | 実装Issue | AssigneeがPull Requestを見て、マージ判断をする |
| `awaiting-blocker-merge` | 実装Issue | blocked byのIssueのPull Requestのマージを待つ。このIssueには、誰も作業しない（[マージ](../merge/index.md)の`CWK-MERGE-004`）|
| `merging` | 実装Issue | cuminがPull Requestをマージし、実装Issueを閉じる |
| `awaiting-decision` | 要求Issue、実装Issue | Assigneeが答える。cuminが先に進めない理由と、Assigneeが選べることは、Issueのコメントにある（[判断の依頼](../ask/index.md)） |

- 検証: `inspection` ラベルの名前がコードの定義と一致すること

---

#### `CWK-LABEL-003` 状態ラベルは1つ

1つのIssueに付く状態ラベルは、常に1つだけである。状態ラベルを替えるのはcuminである。
例外は`cumin/status/ready`で、これはMaintainerやAssigneeがつける。またタスク再開指示でもあるので状態ラベルの重複も許可する。

- 検証: `test` GitHub mockでテスト
- 検証: `inspection` Issueの状態ラベルを書き込むコードが一箇所だけである

---

#### `CWK-LABEL-004` cuminへの再開指示

MaintainerやAssigneeがタスクを終えてcuminにタスクを再開してほしいときは、`ready`ラベルを該当Issueに追加する

- 検証: `test` GitHub mockでテスト。`ready`とMaintainerやAssigneeにアクションが求められる状態ラベルは同時についていてもエラーにならない。各タイミングの振る舞いはそれぞれのカテゴリで定義する

---

### リスクのラベル

#### `CWK-LABEL-005` リスクのラベル

実装Issueには、`cumin/risk/low`、`cumin/risk/medium`、`cumin/risk/high`のどれか1つが付く。Plannerが仮に付け、要求IssueのAssigneeが確かめて、必要なら替える。

- 検証: `inspection` ラベルの名前がコードの定義と一致すること

---

### 優先度のラベル

#### `CWK-LABEL-006` 優先度のラベル

優先度のラベルは、着手の順番を表す。初期値は`cumin/priority/P0`から`cumin/priority/P3`で、`P0`が最も高い。
対象のリポジトリにすでにある優先度のラベルを使うときは、`.cumin/config.toml`の`priority_labels`に、名前を高い順に書く。

優先度のラベルは、要求Issueにも実装Issueにも付けてよい。付けるのはMaintainerだけで、cuminとAgentは付けも外しもせず、Pull Requestにも写さない。着手の順番は[定期確認](../poll/index.md)の`CWK-POLL-006`で決める。

- 検証: `inspection` ラベルの名前がコードの定義と一致すること

---

### Pull Request

#### `CWK-LABEL-007` ラベルをPull Requestに写す

cuminは、実装Issueの状態ラベルとリスクのラベルを、そのIssueを閉じるPull Requestに写す。MaintainerがPull Requestの一覧で見分けるためのもので、cuminは読まない。Pull Requestの側で替えても、実装Issueのラベルで上書きする。

- 検証: `test` GitHub mockでテスト

---

### リポジトリ

#### `CWK-LABEL-008` ラベルを作る

cuminは、対象のリポジトリに足りないラベルを作る。すでにあるラベルの色と説明は変えない。
`priority_labels`で設定した優先度のラベルは、対象のリポジトリがすでに持つラベルなので、作らない。

- 検証: `test` GitHub mockでテスト
- 検証: `demo` テスト用の実リポジトリで確認

---

## 未決事項

なし
