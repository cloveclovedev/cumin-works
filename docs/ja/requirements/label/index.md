# ラベル

Maintainer と cumin の間の取り決めとして、Issue と Pull Request に付けるラベルを決める。Maintainer はラベルを見て状態を知り、ラベルを付けて cumin に合図する。状態ごとのアクションは、計画、実装、レビュー、マージ、受け入れの各カテゴリで決める。誰がどのラベルを付けてよいかは [権限](../access/index.md) で決める。

## 全体像

ラベルには4つの種類があり、名前の前半で見分けられる。優先度のラベルだけは、リポジトリの設定で名前を変えられる。

- `cumin/type/*`: Issue の種類。状態ではないので、閉じるまで付けたままにする。
- `cumin/status/*`: Issue の状態。
- `cumin/risk/*`: 実装Issueのマージのリスク。
- `cumin/priority/*`: 着手の順番 ([定期確認](../poll/index.md))。

状態は3つの種類に分かれ、名前で見分けられる。

- 人なしで進む状態: 名前が `-ing` で終わる。Agent、GitHub、cumin のどれかが作業している。
- 人の番の状態: 名前が `awaiting-` で始まる。Assignee が動くまで進まない。
- 順番を待つ状態: `ready`。Maintainer が進めてよいと合図し、cumin が空きを待っている。

状態がどう移るかの全体は、[要求と要件の入口](../index.md) の全体像の図にある。

## 要件

### 状態のラベル

#### `CWK-LABEL-001` 状態の一覧

Issue の状態は、次のどれかである。状態ラベルの名前は、`cumin/status/` のあとに状態の名前を付けたものである。

| 状態 | 付く対象 | その状態で進むこと |
|---|---|---|
| `ready` | 要求Issue、実装Issue | cumin が空きを待つ |
| `planning` | 要求Issue | Planner が計画する |
| `awaiting-plan-review` | 要求Issue | Assignee が計画結果を確かめる |
| `implementing` | 要求Issue | sub-issue の作業が進む。要求Issueそのものには、誰も作業しない |
| `accepting` | 要求Issue | Planner が受け入れ確認をする |
| `awaiting-acceptance` | 要求Issue | Assignee が、受け入れるか差し戻すかを決める |
| `implementing` | 実装Issue | Implementer が作業する (修正を含む) |
| `checking` | 実装Issue | GitHub が必須チェックを動かす |
| `reviewing` | 実装Issue | Reviewer が Pull Request をレビューする |
| `awaiting-merge-decision` | 実装Issue | Assignee が Pull Request を見て、マージ判断をする |
| `merging` | 実装Issue | cumin が Pull Request をマージし、実装Issueを閉じる |
| `awaiting-decision` | 要求Issue、実装Issue | Assignee が答える。cumin が先に進めない理由は、Issue のコメントにある |

- 検証: `test` cumin が作るラベルの一覧が、この表の状態と一致する

---

#### `CWK-LABEL-002` 状態ラベルは1つ

1つの Issue に付く状態ラベルは、常に1つだけである。状態ラベルを替えるのは cumin である。例外は `cumin/status/ready` で、これは Maintainer が付ける。

- 検証: `test` cumin がラベルを替えたあと、どの Issue にも状態ラベルが1つだけ付いている

---

#### `CWK-LABEL-003` ドラフトは扱わない

状態ラベルのない Issue (ドラフト) は、cumin が扱わない。

- 検証: `test` 状態ラベルのない Issue に、cumin は何もしない

---

### 種類、リスク、優先度のラベル

#### `CWK-LABEL-004` 種類のラベル

`cumin/type/requirement` は、その Issue が要求Issueであることを表す。Maintainer が付け、閉じるまで付けたままにする。`cumin/type/requirement` の付いた Issue の sub-issue が、実装Issueである。実装Issueを表すラベルは作らない。

`cumin/type/owner-task` は、その実装Issueの作業を Assignee が手で行うことを表す。Implementer が変えられないファイル (保護されたパス、GitHub App の権限で書けないファイル) の変更が要るときに、Planner か Maintainer が付ける。cumin は、`cumin/status/ready` が付いていても、この Issue に着手しない。

- 検証: `test` `cumin/type/owner-task` と `cumin/status/ready` の付いた実装Issueに、Agent起動をしない

---

#### `CWK-LABEL-005` リスクのラベル

実装Issueには、`cumin/risk/low`、`cumin/risk/medium`、`cumin/risk/high` のどれか1つが付く。Planner が仮に付け、Maintainer が確かめて、必要なら替える。

- 検証: `test` リスクのラベルがないか、2つ以上ある実装Issueがあると、計画が確認を通らない

---

#### `CWK-LABEL-008` 優先度のラベル

優先度のラベルは、着手の順番を表す。初期値は `cumin/priority/P0`、`cumin/priority/P1`、`cumin/priority/P2`、`cumin/priority/P3` で、`P0` が最も高い。対象のリポジトリに優先度のラベルがすでにあるときのために、`.cumin/config.toml` の `priority_labels` で、ラベルの名前を高い順に設定できる。設定があれば、cumin は初期値ではなく、設定のラベルに従う。Host の設定ファイルでは設定できない。

優先度のラベルは、要求Issueにも実装Issueにも付けてよく、付けなくてもよい。付けるのは Maintainer だけである。cumin と Agent は、付けも外しもせず、Pull Request にも写さない。この順番で着手する決まりは [定期確認](../poll/index.md) で決める。

- 検証: `test` `priority_labels` を設定したリポジトリでは、設定のラベルの順に着手する。設定のないリポジトリでは、初期値のラベルの順に着手する
- 検証: `test` cumin と Agent が作業したあとも、Issue の優先度のラベルは変わらず、Pull Request には優先度のラベルが付かない

---

### Pull Request とリポジトリ

#### `CWK-LABEL-006` ラベルを Pull Request に写す

cumin は、実装Issueの状態ラベルとリスクのラベルを、その Issue を閉じる Pull Request にも写す。写したラベルは、Maintainer が Pull Request の一覧で状態とリスクを見分けるためのもので、cumin は読まない。Pull Request の側でラベルを替えても、cumin が実装Issueのラベルで上書きする。この写しは、どの状態でも行い、状態を変えない (copy the labels to the pull request)。

- 検証: `test` cumin が実装Issueのラベルを替える、Maintainer が実装Issueのリスクを替える、Maintainer が Pull Request の側のラベルを替える、のどれのあとでも、次の定期確認で Pull Request のラベルが実装Issueと同じになる。cumin のアクションは、Pull Request のラベルに左右されない

---

#### `CWK-LABEL-007` ラベルを作る

cumin は、対象のリポジトリに足りないラベルを作る。すでにあるラベルの色と説明は変えない。`priority_labels` で設定した優先度のラベルは、対象のリポジトリか Organization がすでに持つラベルなので、作らない。

- 検証: `test` ラベルのないリポジトリで、cumin がこのページのラベルを作る。すでにあるラベルの色と説明は変わらない

---

## 未決事項

なし
