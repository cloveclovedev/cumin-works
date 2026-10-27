# Agent

Agent起動に共通のことを決める。起動の手順、渡す指示と事実、結果、セッション、時間の上限、異常終了、ツール動作確認、どのroleも守ることを扱う。roleごとの起動の条件と成果物は各カテゴリのページ、Agentの権限は[権限](../access/index.md)、利用制限は[利用制限](../limit/index.md)で決める。

## 全体像

1回のAgent起動で、実行がHostの上でたどる流れを示す。GitHubのラベルではない。

![Agentの全体像](overview.svg)

図の元ファイル: [overview.puml](overview.puml)

実行のあとに何をするかは、cuminがGitHubの事実から決める。そのため、異常終了も、何も残さなかった実行も、cuminの再起動で切れた実行も、同じに扱える。

## 要件

### Agent起動の手順

#### `CWK-AGENT-001` Agentが動いていること

あるIssueで あるroleの「Agentが動いている」とは、このcuminが、その実行を今持っていることである。各ページの「Plannerが動いていない」などは、この意味である。cuminを再起動すると、どのAgentも動いていないとして扱う。

- 検証: `test` 実行の途中でcuminを再起動すると、次の定期確認で各ページのやり直しが起きる

---

#### `CWK-AGENT-024` すべてのAgent起動の前の確認

cuminは、どのAgent起動の前にも、同じ1つの確認を行う。確認するのは、利用制限（`CWK-LIMIT-007`）、そのリポジトリのツール動作確認（`CWK-AGENT-018.3`）、止める予約（`CWK-HOST-007`）である。新しい種類のAgent起動を足しても、この確認を飛ばせない。

- 検証: `test` 利用制限に達している間は、どの種類のAgent起動も起きない
- 検証: `inspection` Agentを起動するコードの呼び出し元が1つだけで、その前に確認がある

---

#### `CWK-AGENT-002` 状態を先に替える

cuminは、Agent起動の前に状態ラベルを替える。替えられなければ起動しない。1つのIssueで、同時に2つのAgentを起動しない。

- 検証: `test` 状態ラベルの書き込みが失敗すると、Agent起動は起きない
- 検証: `test` Agentが動いているIssueに、同じAgent起動は起きない

---

#### `CWK-AGENT-003` Agent作業ディレクトリ

cuminは、IssueとroleごとにAgent作業ディレクトリを用意し、その中でAgentを起動する。

- 検証: `test` 同じ実装IssueのImplementerとReviewerが、別のAgent作業ディレクトリで起動される

---

#### `CWK-AGENT-004` Agent作業ディレクトリを片付ける

Issueが閉じ、そのAgentが動いていないとき、cuminはそのIssueのAgent作業ディレクトリとローカルのブランチを消す。コミットしていない変更かpushしていないコミットがあれば、消さない。cuminが読まないIssueのAgent作業ディレクトリは、Operatorが片付ける。

- 検証: `test` マージされて閉じた実装IssueのAgent作業ディレクトリが、次の定期確認で消える
- 検証: `test` pushしていないコミットがあると、消えない

---

#### `CWK-AGENT-005` Agentの環境

cuminは、起動の直前にそのroleのGitHub Appのtokenを発行して、Agentの環境に入れる（[権限](../access/index.md)の`CWK-ACCESS-011`）。tokenの1時間の期限を、実行にまるごと残すためである。Hostのユーザアカウントのgitと`gh`の認証は、Agentから使えなくする。

- 検証: `test` Agentのプロセスで、Hostのユーザアカウントのgitと`gh`の認証が使われない

---

### Agentに渡す指示

#### `CWK-AGENT-006` 指示の合成

cuminは、次の4つをこの順につないで、roleの指示として渡す。

1. roleのファイル（`roles/<role>.md`）
2. 専門知識のファイル（`disciplines/<専門知識>/<role>.md`）
3. 平易な英語の決まり（`templates/writing-rules.md`）
4. 対象のリポジトリのリスクの基準（[Hostでの動かし方](../host/index.md)）

食い違ったら、roleのファイルが勝つ。専門知識のファイルがないroleは、それを飛ばす。今の専門知識は`software-engineering`だけである。

- 検証: `test` 渡す指示が4つの部分をこの順に含む。専門知識のファイルがないroleでは3つになる

---

#### `CWK-AGENT-007` テンプレートをskillで渡す

cuminは、GitHubに残す文章のテンプレート（`templates/`）を、その文章を書くroleにskillとして渡す。Agentは、書く直前にskillを呼ぶ。

- 検証: `test` ImplementerのAgent作業ディレクトリに、Implementerが書く文章のskillがある

---

#### `CWK-AGENT-008` Hostの指示を読ませない

cuminは、AgentがHostのユーザアカウントの指示と設定（Claude Codeでは`~/.claude`、plugin、MCPサーバ、claude.aiのコネクタ）を読まないように起動し、自動メモリも切る。何を根拠に動いたかを追えるようにするためである。対象のリポジトリの指示は読んでよい。

- 検証: `demo` Hostのユーザアカウントに置いた`CLAUDE.md`が、Agentに効かない

---

#### `CWK-AGENT-009` 起動の記録を確かめる

cuminは、実行の最初に出る起動の記録（Claude Codeでは`init`のイベント）を確かめ、次のどれかに当たったら、その場で止めて異常終了とする。

- Agent作業ディレクトリの外の指示かメモリがある
- ユーザアカウントのpluginかMCPサーバがある。CLIに組み込みのpluginは許す
- cuminが渡したskillが1つでも欠けている
- 確かめる項目がないか、読めない
- 起動の記録がないまま、実行が終わった

- 検証: `test` ユーザアカウントのMCPサーバか、組み込みでないpluginがあると、異常終了になる
- 検証: `test` skillが1つ欠けていると、異常終了になる

---

#### `CWK-AGENT-010` Agentに渡す事実

cuminは、依頼の先頭に、次の事実を書く。roleごとに足す事実は、各ページで決める。

- 扱うIssueの番号と種類
- Assigneeのログイン名。いなければ、いないと書く。AgentはAssigneeのコメントだけを指示として読む（[権限](../access/index.md)の`CWK-ACCESS-002`）
- 保護されたパスの一覧と照合の決まり（[チェック](../check/index.md)の`CWK-CHECK-008.1`）
- 時間の上限と、実行が終わる時刻

- 検証: `test` どのroleの依頼にも、4つの事実がある。AssigneeのいないIssueでは、いないと書いてある

---

### 結果

#### `CWK-AGENT-011` 結果の形式

Agentは、実行の最後に、次の3つを持つJSONを返す。CLIが何であっても変わらない。

| 項目 | 内容 |
|---|---|
| `result` | `done`か`blocked` |
| `summary` | 何をしたかを1〜3文で |
| `blocked_reason` | `blocked`のとき、[decision-request.md](../../../../templates/decision-request.md)の形式の判断の依頼。`done`のときは空 |

cuminはJSONを確かめ、形式に合わないときや、`blocked`で`blocked_reason`が空のときは、異常終了とする。

- 検証: `test` 項目の足りないJSONと、`blocked_reason`が空の`blocked`は、異常終了になる

---

#### `CWK-AGENT-012` doneは確かめ始めの合図

`done`は、GitHubを確かめ始めてよいという合図でしかない。作業が済んだかは、各ページの完了判断で決める。

- 検証: `test` `done`を返したが何も残さなかったAgentのあとで、各ページのやり直しが起きる

---

#### `CWK-AGENT-013` 質問のコメント

Agentが`blocked`を返したら、cuminは`blocked_reason`を、扱ったIssue（ReviewerではPull Request）にすぐコメントとして書く。これが各ページの「質問のコメント」である。

- 検証: `test` `blocked`のあとで、`blocked_reason`がコメントになり、`cumin/status/awaiting-decision`になって、通知が1回届く

---

### セッションと時間

#### `CWK-AGENT-014` セッション

Assigneeが間に入らない一続きの作業は、同じセッションで続ける。Assigneeが`cumin/status/ready`を付け直したあとは、新しいセッションにする。セッションの番号はHostに持ち、失ったら新しいセッションで起動する。異常終了した実行のセッションは残さない。

- 検証: `test` チェックの修正は直前のセッションで、`cumin/status/ready`の付け直しのあとは新しいセッションで起動する
- 検証: `test` セッションの番号を消すと、続きが新しいセッションで起動する

---

#### `CWK-AGENT-015` 実行の時間の上限

1回の実行には、roleごとの時間の上限（`roles.<role>.time_limit`、初期値は50分、最大55分）がある。上限を過ぎたら、cuminはAgentが起動したコマンドまで止め、異常終了とする。最大を55分にするのは、tokenが1時間で失効するためである。

- 検証: `test` 上限を過ぎた実行は止まり、Agentが起動したコマンドも残らない
- 検証: `test` 55分を超える`time_limit`は、設定の誤りになる

---

#### `CWK-AGENT-016` 終わる時刻に合わせる

Agentは、長い確認を、終わる時刻より十分前に終える。終わらないときは、収まる分で打ち切り、行った回数と結果をPull Requestの「How it was checked」に、足りない分を`Follow-up`に書く。完了の条件そのものが時間に収まらないときは、`blocked`を返す。この決まりは、各roleの専門知識のファイルに書く。

- 検証: `inspection` 各roleの専門知識のファイルに、この決まりがある
- 検証: `demo` 時間に収まらない繰り返しを求める実装Issueで、打ち切った回数と結果がPull Requestにある

---

### 異常終了

#### `CWK-AGENT-017` 異常終了

CLIのプロセスの失敗、結果がないか形式に合わないこと、起動の記録の問題、時間の上限は、異常終了である。cuminは、異常終了をその場でやり直さず、何も残さなかった実行として扱う。次に何をするかは、各ページの完了判断がGitHubの事実から決める。

- 検証: `test` 異常終了したImplementerのあとで、やり直しが1回だけ起き、2回目も異常終了すると`cumin/status/awaiting-decision`になる

---

### ツール動作確認

#### `CWK-AGENT-018` ツール動作確認

対象のリポジトリが求めるツールがHostで動くかを、cuminがAgent起動の前に確かめる。動かないツールでAgentが作業しないためである。

##### `CWK-AGENT-018.1` 確認の書き方

確認は、対象のリポジトリの`.cumin/config.toml`の`tool_checks`に書く。1つの確認は、コマンド（`command`、文字列の配列。シェルは通さない）と、時間の上限（`time_limit`、60秒以下、初期値は10秒）を持つ。Maintainerが承認したコマンドだけをHostで実行するために、保護されたパスの`.cumin/`に置く。

- 検証: `test` 空の`command`と、60秒を超える`time_limit`は、設定の誤りになる

---

##### `CWK-AGENT-018.2` 確認の実行

cuminは、定期確認のたびに、Agent起動を決める前に、すべての確認を書いた順に実行する。終了コードが0で、時間の上限の中に終わったときだけ成功とする。Agentと同じ環境変数で実行し、tokenは渡さない。

- 検証: `test` 見つからないコマンド、0でない終了コード、時間の上限を過ぎたコマンドは、失敗になる

---

##### `CWK-AGENT-018.3` 止める範囲

確認が1つでも失敗している間、cuminは、そのリポジトリのAgent起動だけを止める。ほかのリポジトリと、Agentを起動しないアクション（マージなど）は続ける。すべて成功すれば、次の定期確認からAgent起動を続ける。ツールを直すのはOperatorである。

失敗し始めたとき、cuminは、リポジトリの名前と失敗した確認を1回だけ通知する。すべて成功したあとにまた失敗したら、もう一度通知する。

- 検証: `test` 失敗する確認があるリポジトリではAgentを起動しないが、マージは行う。ほかのリポジトリでは起動する
- 検証: `test` 失敗したまま定期確認を3回行っても、通知は1回だけである

---

### CLI

#### `CWK-AGENT-019` CLIの違いを閉じ込める

Agentを動かすCLIは、今はClaude Codeだけである。CLIごとの違いは、cuminの中のCLIの接続部分1つに閉じ込める。

- 検証: `inspection` CLIのコマンド名とオプションが、接続部分のパッケージの外にない

---

### どのroleも守ること

#### `CWK-AGENT-020` ラベルを替えない

Agentは、`cumin/*`のラベルを替えない。例外は、Plannerがsub-issueにリスクのラベルと`cumin/type/owner-task`を付けること、やり直しでリスクのラベルを直すことだけである。

- 検証: `demo` ImplementerとReviewerの実行のあとで、ラベルがAgentによって変わっていない

---

#### `CWK-AGENT-021` 保護されたパスを変えない

Agentは、保護されたパスのファイルを変えない。変える必要があれば、`blocked`を返す（[実装](../impl/index.md)の`CWK-IMPL-014.2`）。

- 検証: `demo` `CLAUDE.md`の変更を求める実装Issueで、Implementerは変更せずに`blocked`を返す

---

#### `CWK-AGENT-022` 推測で進めない

Agentは、決まっていないことに当たったら、`blocked`を返す。何で止まるかは、各ページで決める。

- 検証: `inspection` 各roleのファイルに、この決まりがある

---

#### `CWK-AGENT-023` GitHubに残す文章

Agentは、GitHubに残す文章と`summary`、`blocked_reason`を、平易な英語で書き、テンプレートに従う。

- 検証: `demo` ImplementerのPull Requestの説明が、英語で、テンプレートの見出しをすべて持つ

---

## 受け入れテスト

| # | 場面 | 期待する結果 |
|---|---|---|
| 1 | cuminがAgentを起動する | 先に状態ラベルが替わり（`CWK-AGENT-002`）、専用のAgent作業ディレクトリで、roleのtokenだけを持ち（`CWK-AGENT-003`、`CWK-AGENT-005`）、4つの部分の指示と事実を受け取る（`CWK-AGENT-006`、`CWK-AGENT-010`）|
| 2 | HostのユーザアカウントにMCPサーバがある状態で起動する | 起動の記録で止まって異常終了になり（`CWK-AGENT-009`）、やり直しのあとも同じなら止まって通知が届く（`CWK-AGENT-017`）|
| 3 | Agentが`blocked`を返す | 質問のコメントになり、やり直さずに止まる（`CWK-AGENT-013`）|
| 4 | 実行が時間の上限を過ぎる | コマンドまで止まり（`CWK-AGENT-015`）、各ページのやり直しが起きる（`CWK-AGENT-017`）|
| 5 | ツール動作確認が失敗する | そのリポジトリのAgent起動だけが止まる（`CWK-AGENT-018.3`）|
| 6 | 実装Issueがマージされて閉じる | Agent作業ディレクトリが片付く（`CWK-AGENT-004`）|

## 未決事項

なし
