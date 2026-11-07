# 権限

人、cumin、Agentが、それぞれどんな権限を持ち、その権限をどう作るかを決める。cuminは、権限のない人の操作では動かない。Agentには、そのroleの作業に要る最小の権限だけを渡す。

## 全体像

| 誰 | 何をしてよいか | 権限の定義 |
|---|---|---|
| Maintainer | 要求Issueを書く | 対象リポジトリの権限設定 |
| Assignee | そのIssueの指示と判断 | 対象リポジトリの権限設定 |
| Operator | Hostの上のcuminの操作 | Hostのログインアカウントの権限設定 |
| cumin | 状態ラベルの付け替え、マージ、Agentの起動 | `cumin-core`のGitHub Appの権限とruleset |
| Agent | そのroleの作業 | roleごとのGitHub Appの権限とruleset。Host上の権限は、cuminを動かすユーザアカウントの権限設定 |

## 要件

### 権限の種類

#### `CWK-ACCESS-001` Maintainer

Maintainerは、対象のリポジトリにwrite以上（write、maintain、admin）の権限を持つ、人のアカウントである。botのアカウントは、権限があってもMaintainerではない。cuminのGitHub AppもMaintainerにならない。Maintainerの一覧は設定に持たず、GitHubの権限から、その都度決める。

- 検証: `test` GitHub mockでテスト
- 検証: `inspection` Maintainerを判定するコードが1箇所だけ

---

#### `CWK-ACCESS-002` Assignee

Assigneeは、IssueにアサインされたMaintainerである。そのIssueの指示と判断を受け持つ。
MaintainerはIssueごとに1人とする。

- 検証: `test` GitHub mockでテスト

---

#### `CWK-ACCESS-003` Operator

Operatorは、Hostの上のコマンド、設定ファイル、Keychain、cuminの版の入れ替えを受け持つ（[Hostでの動かし方](../host/index.md)）。リポジトリの権限とは関係がない。

- 検証: `inspection` Hostの上のコマンドが、GitHubの権限を確かめずに動くことを、コマンドの一覧で確かめる

---

#### `CWK-ACCESS-004` cuminの権限

cuminは、GitHubの上では`cumin-core`のGitHub Appとして振る舞う。持つ権限は次の6つである。

- コードの読み書き（マージに必要）
- Pull Requestの読み書き
- Issueの読み書き
- チェックの読み取り
- commit statusの読み取り
- GitHub Actionsのjobのログの読み取り

読み取りの3つは、privateのリポジトリで、必須チェックとその失敗の内容を読むのに必要である。

mainブランチを更新できるのは、rulesetによって、リポジトリの管理者と`cumin-core`だけに限る。

cuminは、人の認証情報（HostにあるOperatorの`gh`のログインなど）と、リポジトリの管理者の権限（Administration）を使わない。

- 検証: `inspection` `cumin-core`のGitHub Appの権限が、この6つと一致し、cuminのGitHubの呼び出しがすべてGitHub Appのtokenで行われる

---

#### `CWK-ACCESS-005` roleごとの権限

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

#### `CWK-ACCESS-006` HostでのAgentの権限

Agentは、Hostの上では、cuminを動かすユーザアカウントと同じ権限を持ち、ツールの使用をすべて許可するモードで起動する。

- 検証: `inspection` Agentがcuminと同じユーザアカウントで動き、起動のコマンドが許可を尋ねないモードである

---

### 権限の設定方法

#### `CWK-ACCESS-007` GitHub Appを作る

Operatorは、`cumin setup github-apps`で、`cumin-core`とroleごとのGitHub Appを作る。各GitHub Appの権限は、`CWK-ACCESS-004`と`CWK-ACCESS-005`の権限だけにする。秘密鍵の置き場所は[Hostでの動かし方](../host/index.md)で決める。

- 検証: `demo` テスト用の実リポジトリで、作ったGitHub Appの権限を確かめる

---

#### `CWK-ACCESS-008` rulesetを作る

rulesetのような、リポジトリの管理者の権限が要る準備は、管理者が自分の`gh`でスクリプトを実行して行う。cuminは、この準備をしない。

- 検証: `demo` テスト用の実リポジトリで、スクリプトがrulesetを作る

---

#### `CWK-ACCESS-009` Agentにtokenを渡す

Agentは、自分のroleのGitHub Appとして振る舞う。cuminは、Agent起動のたびに、そのroleのGitHub AppのtokenだけをAgentに渡す。tokenは1時間で失効する。Agentは、人の認証情報を使わない。

- 検証: `test` Agentのプロセスの環境に、そのroleのGitHub Appのtokenのほかに、GitHubの認証情報がない

---

#### `CWK-ACCESS-010` Assigneeを付ける

`cumin/status/ready`の付いたIssueにassigneeがいなければ、cuminは、その`cumin/status/ready`を付けたMaintainerをassigneeにする。

- 検証: `test` GitHub mockでテスト

---

#### `CWK-ACCESS-011` Assigneeを替える

担当を替えるときは、Maintainerがassigneeを替える。assigneeを替えても、Agentは動き直さない。次の判断は、新しいAssigneeに求める。

- 検証: `test` GitHub mockでテスト

---

### 権限の確かめ方

#### `CWK-ACCESS-012` 各操作元の権限チェック

cuminは、GitHubへの各操作を読み取るとき、適切な権限を持ったものが行った操作かをチェックする。誰が行ったかは、GitHubがIssueのタイムラインに残すイベントと、レビューとコメントの作者から読む。

| 種類 | 権限のあるアカウント |
|---|---|
| 状態ラベルを付ける | `cumin/status/ready`は、要求IssueではMaintainer、実装Issueでは要求IssueのAssigneeかその実装IssueのAssignee。ほかは`cumin-core`のGitHub App |
| assigneeを替える | Maintainer |
| 判断と指示（承認、変更の依頼、受け入れ、指示のコメントなど） | そのIssueのAssignee |

どの操作が判断と指示に当たるかは、各カテゴリの要件で決める。

- 検証: `test` GitHub mockで、種類ごとに、権限のあるアカウントとないアカウントの操作をテスト

---

#### `CWK-ACCESS-013` 権限に不一致のあるIssue

次のどれかに当たるIssueでは、cuminはAgentを起動せず、マージもラベルの付け替えもしない。ログと通知は1回だけ残す。

- 権限外のアカウントが状態ラベルを付けたか、assigneeを替えた
- assigneeが2人以上いる
- assigneeがMaintainerでない

適切な操作で直されると、cuminはそのIssueを再開する。

- 検証: `test` GitHub mockでテスト

---

#### `CWK-ACCESS-014` 権限外の操作を無視する

権限外のアカウントによる判断と指示を、cuminは使わない。Issueは止めず、通知もしない。publicのリポジトリでは、誰でもコメントやレビューを書けるからである。

- 検証: `test` GitHub mockでテスト

---

#### `CWK-ACCESS-015` rulesetを確かめる

cuminは、リポジトリでAgentを起動する前とマージの前に、`CWK-ACCESS-004`と`CWK-ACCESS-005`で決めた制限のrulesetが、そのリポジトリに効いているかを確かめる。効いていなければ、そのリポジトリでAgentを起動せず、マージもしない。通知は1回だけ送る。

- 検証: `test` GitHub mockでテスト
- 検証: `demo` テスト用の実リポジトリで、rulesetを外すと止まる

---

## 未決事項

なし
