# 権限

人、cumin、Agent が、それぞれ何をしてよいかを決める。cumin は、権限のない人の操作では動かない。Agent には、その role の作業に要る最小の権限だけを渡す。

## 全体像

| 誰 | 何をしてよいか | どこで決まるか |
|---|---|---|
| Maintainer | 要求Issueを書き、`cumin/status/ready` で合図する | 対象のリポジトリの権限 |
| Assignee | その Issue の指示と判断 | Issue の assignee |
| Operator | Host の上の cumin の操作 | Host |
| cumin | 状態ラベルの付け替え、マージ | `cumin-core` の GitHub App の権限と ruleset |
| Agent | その role の作業 | role ごとの GitHub App の権限と ruleset。Host では、cumin を動かすユーザアカウント |

## 要件

### 人

#### `CWK-ACCESS-001` Maintainer

Maintainer は、対象のリポジトリに write 以上 (write、maintain、admin) の権限を持つ、人のアカウントである。bot のアカウントは、権限があっても Maintainer ではない。cumin の GitHub App も Maintainer にならない。Maintainer の一覧は設定に持たず、GitHub の権限から、その都度決める。

- 検証: `test` write の権限を持つ人のアカウントは Maintainer とし、read と triage の権限のアカウントと、GitHub App は Maintainer としない

---

#### `CWK-ACCESS-002` 人がすること

Maintainer と Assignee は、次のことをする。

| すること | 誰がするか |
|---|---|
| 要求Issueを書き、`cumin/type/requirement` を付ける | Maintainer |
| `cumin/status/ready` を付けて、進めてよいと合図する | Maintainer |
| 要求Issueどうしに blocked by を張り、優先度のラベルを付ける | Maintainer |
| Agent への指示を、Issue のコメントに書く | その Issue の Assignee |
| 計画結果の確認 (sub-issue に `cumin/status/ready` を付ける、要らない sub-issue を閉じる) | 要求Issueの Assignee |
| マージ判断 (Pull Request の承認と、変更の依頼) | 実装Issueの Assignee |
| 受け入れ (要求Issueを閉じる) と差し戻し | 要求Issueの Assignee |
| `cumin/status/awaiting-decision` の Issue に答える (答えを書き、`cumin/status/ready` を付け直す) | その Issue の Assignee |
| `cumin/type/owner-task` の作業をして、その Issue を閉じる | その Issue の Assignee |
| 状態ラベルを手で直す | Maintainer |

Agent は、Assignee のコメントだけを指示として読む。cumin は、判断を Assignee に求め、Assignee の判断だけを扱う。

- 検証: `test` 実装Issueの Assignee でない Maintainer が Pull Request を承認しても、cumin はマージしない。Assignee が承認するとマージする

---

#### `CWK-ACCESS-003` Operator

Operator は、Host の上のコマンド、Host の設定ファイル、Keychain、バイナリの入れ替えを受け持つ。対象のリポジトリの権限とは関係がなく、Operator が Maintainer でないこともある。

- 検証: `inspection` Host の上のコマンドが、GitHub の権限を確かめずに動くことを、コマンドの一覧で確かめる

---

### 扱う状態ラベル

#### `CWK-ACCESS-004` 状態ラベルを付けたアカウント

cumin は、今付いている状態ラベルを最後に付けたアカウントを確かめ、次のときだけ、その状態ラベルを扱う。付けたアカウントは、GitHub が Issue のタイムラインに残すラベルのイベントから読む。

| 状態ラベル | 扱うのは、誰が付けたときか |
|---|---|
| `cumin/status/ready` | Maintainer |
| ほかのすべての状態ラベル | `cumin-core` の GitHub App か、Maintainer |

- 検証: `test` triage の権限のアカウントが `cumin/status/planning` や `cumin/status/merging` を付けても、cumin は Agent起動もマージもせず、ラベルも替えない。`cumin-core` か Maintainer が付けた状態ラベルでは、今までどおり動く

---

#### `CWK-ACCESS-005` 扱わない状態ラベル

扱わない状態ラベルの付いた Issue では、cumin は何もしない。Agent起動をせず、マージせず、ラベルも替えない。ログと通知を1回だけ残し、同じラベルについて、定期確認のたびに繰り返さない。Maintainer が正しいラベルを付け直すと、今までどおり進む。

- 検証: `test` Maintainer でないアカウントが `cumin/status/ready` を付けても着手せず、通知は1回だけである。Maintainer が付け直すと着手する

---

### Assignee

#### `CWK-ACCESS-006` Assignee を付ける

`cumin/status/ready` の付いた Issue に assignee がいなければ、cumin は、その `cumin/status/ready` を付けた Maintainer を assignee にする。

- 検証: `test` assignee のない Issue に Maintainer が `cumin/status/ready` を付けると、その人が assignee になる

---

#### `CWK-ACCESS-007` Assignee は1人の Maintainer

Issue を進められるのは、assignee がちょうど1人で、その人が Maintainer のときだけである。assignee が2人以上いるとき、または assignee が Maintainer でないとき、cumin はその Issue を進めず、通知を1回だけ送る。

- 検証: `test` assignee が2人いる Issue と、Maintainer でない assignee の Issue に、cumin は着手せず、それぞれ通知は1回だけである

---

#### `CWK-ACCESS-008` Assignee を替える

担当を替えるときは、Maintainer が assignee を替える。cumin は、Maintainer が替えた assignee だけを扱う。替えたアカウントは、GitHub が Issue のタイムラインに残す assignee のイベントから読む。assignee を替えても、Agent は動き直さない。

- 検証: `test` Maintainer が assignee を替えると、次の判断はその人に求める。Agent起動は増えない
- 検証: `test` Maintainer でないアカウントが assignee を替えたときは、扱わない

---

### cumin と Agent

#### `CWK-ACCESS-009` cumin の権限

cumin は、GitHub の上では `cumin-core` の GitHub App として振る舞う。持つ権限は、コードの読み書き (マージに要る)、Pull Request の読み書き、Issue の読み書き、チェックと commit status と GitHub Actions の job のログの読み取りである。読み取りの3つは、private のリポジトリで、必須チェックとその失敗の内容を読むのに要る。main ブランチを更新できるのは、ruleset によって、リポジトリの管理者と `cumin-core` だけに限る。

- 検証: `inspection` `cumin-core` の GitHub App の権限が、この6つと一致する

---

#### `CWK-ACCESS-010` cumin が使わない権限

cumin は、人の認証情報 (Host にある Operator の `gh` のログインなど) と、リポジトリの管理者の権限 (Administration) を使わない。ruleset のような、管理者の権限が要る準備は、管理者が自分の `gh` でスクリプトを実行して行う。

- 検証: `inspection` cumin の GitHub の呼び出しが、すべて GitHub App の token で行われ、Administration の権限を求めないことを、コードと App の権限で確かめる

---

#### `CWK-ACCESS-011` Agent に渡す権限

Agent は、自分の role の GitHub App として振る舞う。cumin は、Agent起動のたびに、その role の GitHub App の token だけを Agent に渡す。token は1時間で失効する。Agent は、人の認証情報を使わない。

- 検証: `test` Agent のプロセスの環境に、その role の GitHub App の token のほかに、GitHub の認証情報がない

---

#### `CWK-ACCESS-012` role ごとの権限

role ごとの GitHub App は、次の権限だけを持つ。

| role | GitHub App | 権限 | 権限と ruleset で止めること |
|---|---|---|---|
| Planner | `cumin-planner` | Issue の読み書き、リポジトリの内容の読み書き | `cumin/diagrams` 以外のブランチへの書き込み、Pull Request の作成 |
| Implementer | `cumin-implementer` | コードの読み書き、Pull Request の読み書き、Issue の読み取り | `.github/workflows` の変更 |
| Reviewer | `cumin-reviewer` | Pull Request の読み書き、コードの読み取り、Issue の読み取り | push |

GitHub App の名前は、運用する組織ごとに決めてよい。

- 検証: `demo` Planner が `cumin/diagrams` 以外のブランチへの変更と、Pull Request の作成を試みると、どちらも拒否される
- 検証: `inspection` 各 GitHub App の権限が、この表と一致する

---

#### `CWK-ACCESS-013` Host での Agent の権限

Agent は、Host の上では、cumin を動かす Host のユーザアカウントと同じ権限を持つ。ツールの使用はすべて許可するモードで起動する。headless の実行では、許可を尋ねられても答える人がいないためである。そのため、GitHub の上で role ごとに作業を制限するのは、GitHub App の権限と ruleset である。

- 検証: `inspection` Agent が cumin と同じユーザアカウントで動き、起動のコマンドが許可を尋ねないモードであることを確かめる

---

## 受け入れテスト

| # | 場面 | 期待する結果 |
|---|---|---|
| 1 | Maintainer でないアカウントが、assignee のない Issue に `cumin/status/ready` を付ける。そのあと Maintainer が付け直す | 最初は着手せず、ラベルも assignee も替わらず、通知は1回だけ (`CWK-ACCESS-005`)。付け直すと、その Maintainer が assignee になり (`CWK-ACCESS-006`)、着手する |

## 未決事項

なし
