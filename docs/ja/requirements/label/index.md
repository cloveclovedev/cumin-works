# ラベル

IssueとPull Requestに付けるラベルを決める。Maintainerはラベルで状態を知り、ラベルでcuminに合図する。状態ごとのアクションは各カテゴリで決める。誰がどのラベルを付けてよいかは[権限](../access/index.md)で決める。

## 全体像

ラベルには4つの種類があり、名前の前半で見分けられる。優先度のラベルだけは、リポジトリの設定で名前を変えられる。

- `cumin/type/*`: Issueの種類。状態ではないので、閉じるまで付けたままにする。
- `cumin/status/*`: Issueの状態
- `cumin/risk/*`: 実装Issueのマージのリスク
- `cumin/priority/*`: 着手の順番（[定期確認](../poll/index.md)）

状態は3つの種類に分かれ、名前で見分けられる。

- 人なしで進む状態: 名前が`-ing`で終わる。Agent、GitHub、cuminのどれかが作業している。
- cuminの外を待つ状態: 名前が`awaiting-`で始まる。Assigneeの判断や、ほかのIssueのマージのように、cuminが自分では進められないことを待つ。
- 順番を待つ状態: `ready`。Maintainerが進めてよいと合図し、cuminが空きを待っている。

状態がどう移るかの全体は、[要求と要件の入口](../index.md)の全体像の図にある。

## 要件

### 状態のラベル

#### `CWK-LABEL-001` 状態の一覧

Issueの状態は、次のどれかである。状態ラベルの名前は、`cumin/status/`のあとに状態の名前を付けたものである。

| 状態 | 付く対象 | その状態で進むこと |
|---|---|---|
| `ready` | 要求Issue、実装Issue | cuminが空きを待つ |
| `planning` | 要求Issue | Plannerが計画する |
| `awaiting-plan-review` | 要求Issue | Assigneeが計画結果を確かめる |
| `implementing` | 要求Issue | sub-issueの作業が進む。要求Issueそのものには、誰も作業しない |
| `accepting` | 要求Issue | Plannerが受け入れ確認をする |
| `awaiting-acceptance` | 要求Issue | Assigneeが、受け入れるか差し戻すかを決める |
| `implementing` | 実装Issue | Implementerが作業する（修正を含む）|
| `checking` | 実装Issue | GitHubが必須チェックを動かす |
| `reviewing` | 実装Issue | ReviewerがPull Requestをレビューする |
| `awaiting-merge-decision` | 実装Issue | AssigneeがPull Requestを見て、マージ判断をする |
| `awaiting-blocker-merge` | 実装Issue | blocked byのIssueのPull Requestのマージを待つ。このIssueには、誰も作業しない（[マージ](../merge/index.md)の`CWK-MERGE-020`）|
| `merging` | 実装Issue | cuminがPull Requestをマージし、実装Issueを閉じる |
| `awaiting-decision` | 要求Issue、実装Issue | Assigneeが答える。cuminが先に進めない理由は、Issueのコメントにある |

- 検証: `test` cuminが作るラベルの一覧が、この表の状態と一致する

---

#### `CWK-LABEL-002` 状態ラベルは1つ

1つのIssueに付く状態ラベルは、常に1つだけである。状態ラベルを替えるのはcuminである。例外は`cumin/status/ready`で、これはMaintainerが付ける。

- 検証: `test` cuminがラベルを替えたあと、どのIssueにも状態ラベルが1つだけ付いている

---

#### `CWK-LABEL-003` ドラフトは扱わない

状態ラベルのないIssue（ドラフト）は、cuminが扱わない。

- 検証: `test` 状態ラベルのないIssueに、cuminは何もしない

---

### 種類、リスク、優先度のラベル

#### `CWK-LABEL-004` 種類のラベル

`cumin/type/requirement`は、そのIssueが要求Issueであることを表す。Maintainerが付け、閉じるまで付けたままにする。`cumin/type/requirement`の付いたIssueのsub-issueが、実装Issueである。実装Issueを表すラベルは作らない。

`cumin/type/owner-task`は、その実装Issueの作業をAssigneeが手で行うことを表す。Implementerが変えられないファイル（保護されたパス、GitHub Appの権限で書けないファイル）の変更が要るときに、PlannerかMaintainerが付ける。cuminは、このIssueに着手しない。

- 検証: `test` `cumin/type/owner-task`と`cumin/status/ready`の付いた実装Issueに、Agent起動をしない

---

#### `CWK-LABEL-005` リスクのラベル

実装Issueには、`cumin/risk/low`、`cumin/risk/medium`、`cumin/risk/high`のどれか1つが付く。Plannerが仮に付け、要求IssueのAssigneeが確かめて、必要なら替える。

- 検証: `test` リスクのラベルがないか、2つ以上ある実装Issueがあると、計画が完了したと判断されない

---

#### `CWK-LABEL-008` 優先度のラベル

優先度のラベルは、着手の順番を表す。初期値は`cumin/priority/P0`から`cumin/priority/P3`で、`P0`が最も高い。対象のリポジトリにすでにある優先度のラベルを使うときは、`.cumin/config.toml`の`priority_labels`に、名前を高い順に書く。

優先度のラベルは、要求Issueにも実装Issueにも付けてよい。付けるのはMaintainerだけで、cuminとAgentは付けも外しもせず、Pull Requestにも写さない。着手の順番は[定期確認](../poll/index.md)の`CWK-POLL-006`で決める。

- 検証: `test` `priority_labels`を設定したリポジトリでは、設定のラベルの順に着手する。設定のないリポジトリでは、初期値のラベルの順に着手する
- 検証: `test` cuminとAgentが作業したあとも、Issueの優先度のラベルは変わらず、Pull Requestには優先度のラベルが付かない

---

### Pull Requestとリポジトリ

#### `CWK-LABEL-006` ラベルをPull Requestに写す

cuminは、実装Issueの状態ラベルとリスクのラベルを、そのIssueを閉じるPull Requestに写す。MaintainerがPull Requestの一覧で見分けるためのもので、cuminは読まない。Pull Requestの側で替えても、実装Issueのラベルで上書きする。

- 検証: `test` cuminが実装Issueのラベルを替える、Maintainerが実装Issueのリスクを替える、MaintainerがPull Requestの側のラベルを替える、のどれのあとでも、次の定期確認でPull Requestのラベルが実装Issueと同じになる。cuminのアクションは、Pull Requestのラベルに左右されない

---

#### `CWK-LABEL-007` ラベルを作る

cuminは、対象のリポジトリに足りないラベルを作る。すでにあるラベルの色と説明は変えない。`priority_labels`で設定した優先度のラベルは、対象のリポジトリかOrganizationがすでに持つラベルなので、作らない。

- 検証: `test` ラベルのないリポジトリで、cuminがこのページのラベルを作る。すでにあるラベルの色と説明は変わらない

---

## 未決事項

なし
