# 権限

人、cumin、Agentが、それぞれ何をしてよいかを決める。cuminは、権限のない人の操作では動かない。Agentには、そのroleの作業に要る最小の権限だけを渡す。

## 全体像

| 誰 | 何をしてよいか | どこで決まるか |
|---|---|---|
| Maintainer | 要求Issueを書き、`cumin/status/ready`で合図する | 対象のリポジトリの権限 |
| Assignee | そのIssueの指示と判断 | Issueのassignee |
| Operator | Hostの上のcuminの操作 | Host |
| cumin | 状態ラベルの付け替え、マージ | `cumin-core`のGitHub Appの権限とruleset |
| Agent | そのroleの作業 | roleごとのGitHub Appの権限とruleset。Hostでは、cuminを動かすユーザアカウント |

## 要件

### 人

#### `CWK-ACCESS-001` Maintainer

Maintainerは、対象のリポジトリにwrite以上（write、maintain、admin）の権限を持つ、人のアカウントである。botのアカウントは、権限があってもMaintainerではない。cuminのGitHub AppもMaintainerにならない。Maintainerの一覧は設定に持たず、GitHubの権限から、その都度決める。

- 検証: `test` writeの権限を持つ人のアカウントはMaintainerとし、readとtriageの権限のアカウントと、GitHub AppはMaintainerとしない

---

#### `CWK-ACCESS-002` 人がすること

MaintainerとAssigneeは、次のことをする。

| すること | 誰がするか |
|---|---|
| 要求Issueを書き、`cumin/type/requirement`を付ける | Maintainer |
| `cumin/status/ready`を付けて、進めてよいと合図する | Maintainer |
| 要求Issueどうしにblocked byを張り、優先度のラベルを付ける | Maintainer |
| Agentへの指示を、Issueのコメントに書く | そのIssueのAssignee |
| 計画結果の確認（sub-issueに`cumin/status/ready`を付ける、要らないsub-issueを閉じる）| 要求IssueのAssignee |
| マージ判断（Pull Requestの承認と、変更の依頼）| 実装IssueのAssignee |
| 受け入れ（要求Issueを閉じる）と差し戻し | 要求IssueのAssignee |
| `cumin/status/awaiting-decision`のIssueに答える（答えを書き、`cumin/status/ready`を付け直す）| そのIssueのAssignee |
| `cumin/type/owner-task`の作業をして、そのIssueを閉じる | そのIssueのAssignee |

Agentは、Assigneeのコメントだけを指示として読む。cuminは、判断をAssigneeに求め、Assigneeの判断だけを扱う。

- 検証: `test` 実装IssueのAssigneeでないMaintainerがPull Requestを承認しても、cuminはマージしない。Assigneeが承認するとマージする

---

#### `CWK-ACCESS-003` Operator

Operatorは、Hostの上のコマンド、設定ファイル、Keychain、cuminの版の入れ替えを受け持つ（[Hostでの動かし方](../host/index.md)）。リポジトリの権限とは関係がない。

- 検証: `inspection` Hostの上のコマンドが、GitHubの権限を確かめずに動くことを、コマンドの一覧で確かめる

---

### 扱う状態ラベル

#### `CWK-ACCESS-004` 状態ラベルを付けたアカウント

cuminは、今付いている状態ラベルを最後に付けたアカウントを確かめ、次のときだけ、その状態ラベルを扱う。付けたアカウントは、GitHubがIssueのタイムラインに残すラベルのイベントから読む。

| 状態ラベル | 扱うのは、誰が付けたときか |
|---|---|
| `cumin/status/ready` | Maintainer |
| ほかのすべての状態ラベル | `cumin-core`のGitHub App |

- 検証: `test` triageの権限のアカウントが`cumin/status/planning`や`cumin/status/merging`を付けても、cuminはAgent起動もマージもせず、ラベルも替えない。Maintainerが`cumin/status/merging`を付けたときも同じである。`cumin-core`が付けた状態ラベルでは、今までどおり動く

---

#### `CWK-ACCESS-005` 扱わない状態ラベル

扱わない状態ラベルの付いたIssueでは、cuminはAgent起動もマージもせず、ラベルも替えない。ログと通知は1回だけ残す。Maintainerが付け直すと、今までどおり進む。

- 検証: `test` Maintainerでないアカウントが`cumin/status/ready`を付けても着手せず、通知は1回だけである。Maintainerが付け直すと着手する

---

### Assignee

#### `CWK-ACCESS-006` Assigneeを付ける

`cumin/status/ready`の付いたIssueにassigneeがいなければ、cuminは、その`cumin/status/ready`を付けたMaintainerをassigneeにする。

- 検証: `test` assigneeのないIssueにMaintainerが`cumin/status/ready`を付けると、その人がassigneeになる

---

#### `CWK-ACCESS-007` Assigneeは1人のMaintainer

Issueを進められるのは、assigneeが1人だけで、その人がMaintainerのときだけである。assigneeが2人以上いるとき、またはassigneeがMaintainerでないとき、cuminはそのIssueを進めず、通知を1回だけ送る。

- 検証: `test` assigneeが2人いるIssueと、MaintainerでないassigneeのIssueに、cuminは着手せず、それぞれ通知は1回だけである

---

#### `CWK-ACCESS-008` Assigneeを替える

担当を替えるときは、Maintainerがassigneeを替える。cuminは、Maintainerが替えたassigneeだけを扱う。替えたアカウントは、GitHubがIssueのタイムラインに残すassigneeのイベントから読む。assigneeを替えても、Agentは動き直さない。

- 検証: `test` Maintainerがassigneeを替えると、次の判断はその人に求める。Agent起動は増えない
- 検証: `test` Maintainerでないアカウントがassigneeを替えたときは、扱わない

---

### cuminとAgent

#### `CWK-ACCESS-009` cuminの権限

cuminは、GitHubの上では`cumin-core`のGitHub Appとして振る舞う。持つ権限は、コードの読み書き（マージに要る）、Pull Requestの読み書き、Issueの読み書き、チェックとcommit statusとGitHub Actionsのjobのログの読み取りである。読み取りの3つは、privateのリポジトリで、必須チェックとその失敗の内容を読むのに要る。mainブランチを更新できるのは、rulesetによって、リポジトリの管理者と`cumin-core`だけに限る。

- 検証: `inspection` `cumin-core`のGitHub Appの権限が、この6つと一致する

---

#### `CWK-ACCESS-010` cuminが使わない権限

cuminは、人の認証情報（HostにあるOperatorの`gh`のログインなど）と、リポジトリの管理者の権限（Administration）を使わない。rulesetのような、管理者の権限が要る準備は、管理者が自分の`gh`でスクリプトを実行して行う。

- 検証: `inspection` cuminのGitHubの呼び出しが、すべてGitHub Appのtokenで行われ、Administrationの権限を求めないことを、コードとAppの権限で確かめる

---

#### `CWK-ACCESS-011` Agentに渡す権限

Agentは、自分のroleのGitHub Appとして振る舞う。cuminは、Agent起動のたびに、そのroleのGitHub AppのtokenだけをAgentに渡す。tokenは1時間で失効する。Agentは、人の認証情報を使わない。

- 検証: `test` Agentのプロセスの環境に、そのroleのGitHub Appのtokenのほかに、GitHubの認証情報がない

---

#### `CWK-ACCESS-012` roleごとの権限

roleごとのGitHub Appは、次の権限だけを持つ。

| role | GitHub App | 権限 | 権限とrulesetで止めること |
|---|---|---|---|
| Planner | `cumin-planner` | Issueの読み書き、リポジトリの内容の読み書き | `cumin/diagrams`以外のブランチへの書き込み、Pull Requestの作成 |
| Implementer | `cumin-implementer` | コードの読み書き、Pull Requestの読み書き、Issueの読み取り | `.github/workflows`の変更 |
| Reviewer | `cumin-reviewer` | Pull Requestの読み書き、コードの読み取り、Issueの読み取り | push |

GitHub Appの名前は、運用する組織ごとに決めてよい。

- 検証: `demo` Plannerが`cumin/diagrams`以外のブランチへの変更と、Pull Requestの作成を試みると、どちらも拒否される
- 検証: `inspection` 各GitHub Appの権限が、この表と一致する

---

#### `CWK-ACCESS-013` HostでのAgentの権限

Agentは、Hostの上では、cuminを動かすユーザアカウントと同じ権限を持ち、ツールの使用をすべて許可するモードで起動する。headlessの実行では、許可を尋ねられても答える人がいないためである。roleごとの制限は、GitHub Appの権限とrulesetで行う。

- 検証: `inspection` Agentがcuminと同じユーザアカウントで動き、起動のコマンドが許可を尋ねないモードであることを確かめる

---

## 受け入れテスト

| # | 場面 | 期待する結果 |
|---|---|---|
| 1 | Maintainerでないアカウントが、assigneeのないIssueに`cumin/status/ready`を付ける。そのあとMaintainerが付け直す | 最初は着手せず、ラベルもassigneeも替わらず、通知は1回だけ（`CWK-ACCESS-005`）。付け直すと、そのMaintainerがassigneeになり（`CWK-ACCESS-006`）、着手する |

## 未決事項

なし
