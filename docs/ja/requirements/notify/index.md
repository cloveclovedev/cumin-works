# 通知

cuminが、いつ、何を通知するかを決める。通知は「見に来てほしい」と伝えるだけで、やりとりはIssueとPull Requestで行う。通知先はHostの設定で決まり、誰が読むかはその設定による。そのため、文書には相手の名前を書かない。

## 全体像

通知するのは、人の判断が要るとき、異常な状態のとき、利用制限のしきい値に達したとき、次に処理するIssueがないときの4つである。どれも、同じ原因について1回だけ知らせる。

![通知の全体像](overview.svg)

図の元ファイル: [overview.puml](overview.puml)

## 要件

### 通知するとき

#### `CWK-NOTIFY-001` 人の判断が要るとき

cuminは、Issueを、人の判断が必要な`awaiting-`で始まる状態に替えたときに、通知する。

`awaiting-blocker-merge`は、通知しない。待っているのはblocked byのIssueのPull Requestのマージ判断で、そちらの通知がすでに届いているからである。

- 検証: `test` GitHub mockでテスト

---

#### `CWK-NOTIFY-002` 異常な状態のとき

cuminは、次の異常な状態のときに、通知する。

| 場面 | 要件 |
|---|---|
| 異常な状態のIssueがある。通知には、その理由を含める | `CWK-HOST-013` |
| 使用率を読めない | `CWK-LIMIT-005` |
| ツール動作確認が失敗した | `CWK-AGENT-022.3` |
| rulesetが効いていない | `CWK-ACCESS-015` |
| 定期確認が続けて失敗した | `CWK-POLL-010` |

- 検証: `test` GitHub mockでテスト

---

#### `CWK-NOTIFY-003` 利用制限のしきい値に達したとき

cuminは、利用制限のしきい値に達してAgent起動を止めたときに、通知する（`CWK-LIMIT-005`）。再開したときは通知しない。

- 検証: `test` GitHub mockとCLI mockでテスト

---

#### `CWK-NOTIFY-004` 次に処理するIssueがないとき

動いているAgentがなく、cuminが次に進められるIssueがないとき、cuminは、待っていることを通知する。Agent起動の前の確認（[Agent](../agent/index.md)の`CWK-AGENT-002`）だけで止めているIssueは、進められるIssueとする。止めた原因は`CWK-NOTIFY-002`か`CWK-NOTIFY-003`で知らせているからである。

通知したあとは、cuminのアクションを挟まない限り、再度通知しない。

- 検証: `test` GitHub mockでテスト

---

### 通知の中身と送り方

#### `CWK-NOTIFY-005` 通知の中身

通知には、理由の1行と、対象のIssueかPull Requestがあればそのリンクを入れる。詳しい理由は、Issueのコメントにある。Agentが書いた文が入っても、通知先で全員への呼び出し（`@everyone`など）が起きないようにする。

- 検証: `test` GitHub mockでテスト

---

#### `CWK-NOTIFY-006` 通知を送らない設定

`notify.discord.enabled`が`false`のとき、cuminは通知だけを送らず、ラベルの付け替えとコメントはふだんどおり行う。リポジトリの設定で決めると、そのリポジトリについての通知に効く。通知先はHostの1つだけで、リポジトリからは変えられない。

- 検証: `test` GitHub mockでテスト

---

#### `CWK-NOTIFY-007` 通知の失敗

通知が失敗しても、cuminはログに残すだけで、コメントもラベルも元に戻さない。アクションを決めるのはGitHubの事実で、通知ではないからである。

- 検証: `test` GitHub mockでテスト

---

#### `CWK-NOTIFY-008` 通知の手段

通知の手段は、今はDiscordのwebhookだけである。手段ごとの違いは、cuminの中の1か所に閉じ込め、手段を替えても、通知を決める側は変えない。

- 検証: `inspection` DiscordのAPIの型とURLが、通知の手段のパッケージの外にない

---

## 未決事項

なし
