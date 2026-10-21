# Agent

Agent起動に共通のことを決める。cumin が Agent を起動する手順、Agent に渡す指示と事実、結果の形式、セッション、時間の上限、異常終了、ツール動作確認と、どの role も守ることである。role ごとの Agent起動の条件と成果物は、[計画](../plan/index.md)、[実装](../impl/index.md)、[レビュー](../review/index.md)、[受け入れ](../accept/index.md) で決める。GitHub と Host での Agent の権限は [権限](../access/index.md)、Agent起動を止める利用制限は [利用制限](../limit/index.md) で決める。

## 全体像

1回の Agent起動で、Agent の実行がたどる段階を示す。図の段階は Host の上のもので、GitHub のラベルではない。

![Agent の全体像](overview.svg)

図の元ファイル: [overview.puml](overview.puml)

Agent の実行が終わったあとに何をするかは、cumin が GitHub の事実から決める。Agent が返した結果は、確かめ始める合図でしかない (`CWK-AGENT-012`)。そのため、異常終了も、結果を残さなかった実行も、cumin の再起動で切れた実行も、同じ1つの決まりで扱える。

## 要件

### Agent起動の手順

#### `CWK-AGENT-001` Agent が動いていること

ある Issue で、ある role の「Agent が動いている」とは、この cumin が、その Issue のその role の Agent の実行を今持っていることである。各ページの「Planner が動いていない」「Implementer が動いていない」「Reviewer が動いていない」は、この意味である。cumin を再起動すると、前の cumin の実行は持っていないので、どの Agent も動いていないとして扱う。

- 検証: `test` Agent の実行の途中で cumin を再起動すると、次の定期確認で、その Issue の Agent は動いていないとして扱われ、各ページのやり直しが起きる

---

#### `CWK-AGENT-002` 状態を先に替える

cumin は、Agent起動の前に、Issue の状態ラベルを替える。替えられなければ、Agent を起動しない。1つの Issue で、同時に2つの Agent を起動しない。同じ作業を二重に頼まないためである。

- 検証: `test` 状態ラベルの書き込みが失敗すると、Agent起動は起きない
- 検証: `test` Agent が動いている Issue に、次の定期確認で同じ Agent起動は起きない

---

#### `CWK-AGENT-003` Agent作業ディレクトリ

cumin は、Issue と role ごとに、作業ディレクトリの下に Agent作業ディレクトリ (git worktree) を用意し、その中で Agent を起動する。Agent から見えるのは、この中である。

- 検証: `test` Implementer と Reviewer が、同じ実装Issueで別の Agent作業ディレクトリで起動される

---

#### `CWK-AGENT-004` Agent作業ディレクトリを片付ける

sub-issue が閉じ、その Agent が動いていないとき、cumin は、その sub-issue の Agent作業ディレクトリと、そのローカルのブランチを消す。ただし、Agent作業ディレクトリに GitHub にないもの (コミットしていない変更、push していないコミット) があれば、消さずに残す。cumin が読まない Issue (閉じた要求Issueの sub-issue、要求Issueから外した sub-issue) の Agent作業ディレクトリは、Operator が片付ける。

- 検証: `test` マージされて閉じた実装Issueの Agent作業ディレクトリは、次の定期確認で消える
- 検証: `test` push していないコミットのある Agent作業ディレクトリは、Issue が閉じても消えない

---

#### `CWK-AGENT-005` Agent の環境

cumin は、Agent起動の直前に、その role の GitHub App の token を発行して、Agent の環境に入れる ([権限](../access/index.md) の `CWK-ACCESS-011`)。直前に発行するのは、token の1時間の期限を、実行にまるごと残すためである。Host のユーザアカウントにある git と `gh` の認証の設定は、Agent から使えないようにする。

- 検証: `test` Agent のプロセスの環境で、Host のユーザアカウントの git と `gh` の認証の設定が使われない

---

### Agent に渡す指示

#### `CWK-AGENT-006` 指示の合成

cumin は、Agent起動のたびに、role としての指示を、次の4つをこの順につないだ1つの文章として渡す。

1. role のファイル (`roles/<role>.md`): cumin と Agent の約束
2. 専門知識のファイル (`disciplines/<専門知識>/<role>.md`): その分野の作業の基準
3. 平易な英語の決まり (`templates/writing-rules.md`): Agent が書くすべての文章に効く
4. リスクの基準: 対象のリポジトリごとに決まる文章 ([Host での動かし方](../host/index.md))

専門知識は role に足すだけで、role の決まりを緩めない。食い違ったら role のファイルが勝つ。この決まりは role のファイルに書く。専門知識のファイルがない role は、その部分を飛ばす。今の専門知識は `software-engineering` の1つだけで、どの role もそれを使う。

- 検証: `test` Agent起動で渡す指示が、4つの部分をこの順に含む。専門知識のファイルがない role では、3つになる
- 検証: `inspection` role のファイルに、専門知識より role のファイルが勝つことが書いてある

---

#### `CWK-AGENT-007` テンプレートを skill で渡す

cumin は、GitHub に残す文章のテンプレート (`templates/`) を、その文章を書く role に skill として渡す。Agent は、その文章を書く直前に skill を呼び、テンプレートに従う。テンプレートを指示の本文に入れないのは、使う直前に読ませるほうが、形式を守らせやすいためである。

- 検証: `test` Implementer の Agent作業ディレクトリに、Implementer が書く文章の skill がある

---

#### `CWK-AGENT-008` Host の指示を読ませない

cumin は、Agent が Host のユーザアカウントの指示と設定 (Claude Code では `~/.claude` の `CLAUDE.md`、plugin、MCP サーバ、claude.ai のコネクタ) を読まないように起動する。Agent の自動メモリも切る。読ませると、Agent が何を根拠に動いたのかを追えなくなるためである。対象のリポジトリにある指示 (`CLAUDE.md`、`AGENTS.md`、skill) は読んでよい。次の Agent に引き継ぐことは、GitHub に書く。

- 検証: `demo` Host のユーザアカウントに `CLAUDE.md` を置いても、Agent の実行に、その指示が効かない

---

#### `CWK-AGENT-009` 起動の記録を確かめる

cumin は、Agent の実行の最初に出る起動の記録 (Claude Code では `init` のイベント) を確かめ、次のどれかに当たったら、その場で実行を止めて異常終了 (`CWK-AGENT-017`) とする。追えない指示のもとで、Agent に作業を始めさせないためである。

- Agent作業ディレクトリの外にある指示かメモリが読み込まれている
- Host のユーザアカウントの plugin か MCP サーバがある。CLI に組み込みの plugin は許す
- cumin がその role に渡した skill (`CWK-AGENT-007`) が、1つでも載っていない
- 確かめる項目が記録にないか、読めない形である。CLI が項目の名前を変えたときに、確かめないまま通さないためである
- 起動の記録がないまま、実行が終わった

- 検証: `test` 起動の記録に、ユーザアカウントの MCP サーバか、組み込みでない plugin があると、実行が止まり、異常終了になる
- 検証: `test` 起動の記録の skill の一覧に、cumin が渡した skill が1つ欠けていると、異常終了になる

---

#### `CWK-AGENT-010` Agent に渡す事実

cumin は、Agent起動の依頼の先頭に、その実行の事実を、ラベル付きの行で書く。cumin がすでに知っている事実を、Agent に推測させないためである。どの role にも、次のものを渡す。role ごとに足す事実は、各ページで決める。

- 扱う Issue の番号と、種類 (要求Issueか実装Issueか)
- その Issue の Assignee のログイン名。いなければ、いないと書く。Agent は、Assignee のコメントだけを指示として読む ([権限](../access/index.md) の `CWK-ACCESS-002`)
- 対象のリポジトリの保護されたパスの一覧と、照合の決まり ([チェック](../check/index.md) の `CWK-CHECK-008.1`)
- この実行の時間の上限と、実行が終わる時刻 (`CWK-AGENT-015`)

- 検証: `test` どの role の Agent起動でも、渡す事実に、Issue の番号と種類、Assignee のログイン名、保護されたパス、時間の上限と終わる時刻がある
- 検証: `test` Assignee のいない Issue では、Assignee がいないと書いてある

---

### 結果

#### `CWK-AGENT-011` 結果の形式

Agent は、実行の最後に、次の3つの項目を持つ JSON を1つ返す。項目と意味は cumin と Agent の約束であり、Agent を動かす CLI が何であっても変わらない。CLI にこの形式を守らせる方法 (Claude Code では `--json-schema`) は、CLI ごとの接続部分に閉じ込める (`CWK-AGENT-019`)。

| 項目 | 内容 |
|---|---|
| `result` | `done` か `blocked` |
| `summary` | 何をしたかを1〜3文で書く |
| `blocked_reason` | `blocked` のときは、Assignee に判断を求める文を [decision-request.md](../../../../templates/decision-request.md) の形式で書く。`done` のときは空にする |

cumin は、返った JSON を自分でも確かめる。形式に合わないとき、`blocked` なのに `blocked_reason` が空のときは、異常終了 (`CWK-AGENT-017`) とする。

- 検証: `test` 項目の足りない JSON と、`blocked_reason` が空の `blocked` は、異常終了になる

---

#### `CWK-AGENT-012` done は確かめ始めの合図

cumin は、Agent が返した `done` を、GitHub の事実を確かめ始めてよいという合図としてだけ使う。作業が済んだかどうかは、各ページの完了判断で、GitHub の事実から決める。

- 検証: `test` `done` を返したが GitHub に何も残さなかった Agent のあとで、各ページのやり直しが起きる

---

#### `CWK-AGENT-013` 質問のコメント

Agent が `blocked` を返したら、cumin は、その `blocked_reason` を、扱った Issue (Reviewer では Pull Request) のコメントとして、すぐに書く。これが各ページの「質問のコメント」である。書く前に cumin が止まったときは、コメントがないので各ページのやり直しが起き、Agent がもう一度同じ質問を返す。

- 検証: `test` Agent が `blocked` を返すと、`blocked_reason` がそのままコメントになり、状態が `cumin/status/awaiting-decision` になって、通知が1回届く

---

### セッションと時間

#### `CWK-AGENT-014` セッション

Assignee が間に入らない一続きの作業 (チェックの修正、指摘の修正、衝突の解消など) は、同じセッションで続ける。Assignee が `cumin/status/ready` を付け直したあとは、新しいセッションにする。新しいセッションの Agent は、要ることを GitHub から読み直す。どのセッションで起動するかは、各ページで決める。

cumin は、セッションの番号を Host に持つ。失ったときは、新しいセッションで起動する。異常終了した実行のセッションは残さない。

- 検証: `test` チェックの修正の Agent起動は、直前のセッションで起きる。`cumin/status/ready` を付け直したあとの Agent起動は、新しいセッションで起きる
- 検証: `test` Host に持つセッションの番号を消すと、続きの Agent起動が新しいセッションで起きる

---

#### `CWK-AGENT-015` 実行の時間の上限

1回の実行には、role ごとの時間の上限 (`roles.<role>.time_limit`、初期値は50分) がある。上限を過ぎたら、cumin は実行を打ち切り、異常終了 (`CWK-AGENT-017`) とする。上限は55分を超えられない。GitHub App の token が発行から1時間で失効し、延ばせないためである。打ち切りでは、Agent が起動したコマンドも残さず止める。

- 検証: `test` 時間の上限を過ぎた実行は打ち切られ、Agent が起動したコマンドも残らない
- 検証: `test` 55分を超える `time_limit` は、設定の誤りになる

---

#### `CWK-AGENT-016` 終わる時刻に合わせる

Agent は、長い確認 (テストの繰り返しなど) を、実行が終わる時刻より十分前に終わるように計画し、結果を書く時間 (Pull Request を開く、結果を返す) を残す。予定の確認が終わらないときは、収まる分で打ち切り、何回のうち何回行ったかと結果を Pull Request の「How it was checked」に書き、足りない分を `Follow-up` に書く。完了の条件そのものが実行の時間に収まらない確認を求めているときは、進めずに `blocked` を返す。条件を変えるか、Issue を分けるか、`time_limit` を延ばすかは、Assignee が決める。この決まりは、各 role の専門知識のファイルに書く。

- 検証: `inspection` 各 role の専門知識のファイルに、終わる時刻より前に確認を終える決まりがある
- 検証: `demo` 100回の繰り返しを求める実装Issueで、時間に収まらない分を打ち切り、行った回数と結果が Pull Request に書いてある

---

### 異常終了

#### `CWK-AGENT-017` 異常終了

次のどれかに当たった実行は、異常終了である。

- CLI のプロセスが失敗した
- 結果がないか、形式に合わない (`CWK-AGENT-011`)
- 起動の記録に問題がある (`CWK-AGENT-009`)
- 時間の上限を過ぎた (`CWK-AGENT-015`)

cumin は、異常終了した実行を、その実行の中でやり直さない。結果を残さなかった実行として扱い、次に何をするかは、各ページの完了判断が GitHub の事実から決める。たとえば、何も残っていなければ、各ページのやり直しが1回だけ起き、やり直しのあとも残っていなければ止まる。cumin が止まるときに切った実行も、同じに扱う。Host のスリープで token が切れたときや、通信が途中で切れたときも、異常終了になる。

- 検証: `test` 異常終了した Implementer のあとで、実装のやり直しが1回だけ起き、2回目も異常終了すると、実装Issueが `cumin/status/awaiting-decision` になる

---

### ツール動作確認

#### `CWK-AGENT-018` ツール動作確認

対象のリポジトリの Agent が使うツール (図を描くための Docker など) が Host で使えないと、Agent は別の方法を探したり、ツールなしで作った結果を出したりする。どの Issue がどのツールを使うかは判断なので、cumin は決めない。決まった規則として、リポジトリのツール動作確認が1つでも失敗している間は、そのリポジトリの Agent を起動しない。

##### `CWK-AGENT-018.1` 確認の書き方

ツール動作確認は、対象のリポジトリの `.cumin/config.toml` の `tool_checks` に、確認の配列で書く。1つの確認は、実行するコマンド (`command`、プログラムの名前と引数の文字列の配列。シェルは通さない) と、待つ時間の上限 (`time_limit`、0より大きく60秒以下、初期値は10秒) を持つ。保護されたパスである `.cumin/` に置くのは、cumin が Host で実行するコマンドを、Maintainer が承認したものだけにするためである。

- 検証: `test` 空の `command` と、60秒を超える `time_limit` は、設定の誤りになる

---

##### `CWK-AGENT-018.2` 確認の実行

cumin は、そのリポジトリの定期確認のたびに、Agent起動を決める前に、確認を書いた順にすべて実行する。結果は次の定期確認まで使う。確認は、終了コードが0で、時間の上限の中に終わったときだけ成功とする。確認は、Agent に渡すのと同じ Host の環境変数で、作業ディレクトリを場所として実行し、GitHub App の token は渡さない。

- 検証: `test` 見つからないコマンド、0でない終了コード、時間の上限を過ぎたコマンドは、どれも失敗になる

---

##### `CWK-AGENT-018.3` 止める範囲

確認が1つでも失敗している間、cumin は、そのリポジトリの Agent起動だけを止める。ほかのリポジトリの Agent起動と、Agent を起動しないアクション (ラベルの付け替え、コメント、通知、マージ、Issue を閉じること) は続ける。`cumin/status/ready` の Issue のラベルも替えない。確認がすべて成功するようになると、次の定期確認から、人の操作なしに Agent起動を続ける。cumin は、ツールを入れも起動もしない。直すのは Operator である。

確認が失敗し始めたとき、cumin は、リポジトリの名前と失敗した確認を1回だけ通知する。次に通知するのは、確認がすべて成功したあとに、また失敗したときである。

- 検証: `test` 失敗する確認が1つあるリポジトリでは、Agent を起動せず、`cumin/status/ready` のラベルも替えないが、マージは行う。別のリポジトリでは Agent を起動する
- 検証: `test` 確認が失敗したまま定期確認を3回行っても、通知は1回だけである。確認が成功するようになると、次の定期確認で `cumin/status/ready` の Issue に着手する

---

### CLI

#### `CWK-AGENT-019` CLI の違いを閉じ込める

Agent を動かす CLI は、今は Claude Code だけである。CLI ごとの違い (起動のオプション、結果の形式の守らせ方、起動の記録の読み方) は、cumin の中の CLI ごとの接続部分 1つに閉じ込め、ほかの部分は CLI を知らない。別の CLI を足すときに、各ページの決まりを変えずに済むようにするためである。

- 検証: `inspection` CLI のコマンド名とオプションが、Agent の接続部分のパッケージの外に現れない

---

### どの role も守ること

#### `CWK-AGENT-020` ラベルを替えない

Agent は、`cumin/*` のラベルを付け替えない。例外は、Planner が sub-issue を作るときにリスクのラベルと `cumin/type/owner-task` を付けること ([計画](../plan/index.md)、[受け入れ](../accept/index.md))、Planner がやり直しで sub-issue のリスクのラベルを直すことだけである。

- 検証: `demo` Implementer と Reviewer の実行のあとで、実装Issueのラベルが Agent によって変わっていない

---

#### `CWK-AGENT-021` 保護されたパスを変えない

Agent は、渡された保護されたパス (`CWK-AGENT-010`) のファイルを変えない。変える必要があるときは、`blocked` を返す ([実装](../impl/index.md) の `CWK-IMPL-014.2`)。

- 検証: `demo` `CLAUDE.md` の変更を求める実装Issueで、Implementer は変更せずに `blocked` を返す

---

#### `CWK-AGENT-022` 推測で進めない

Agent は、決まっていないことに当たったら、推測で進めずに `blocked` を返す。何に当たったら止まるかは、各ページの「判断を求めるとき」で決める。

- 検証: `inspection` 各 role のファイルに、推測で進めずに `blocked` を返す決まりがある

---

#### `CWK-AGENT-023` GitHub に残す文章

Agent は、Issue、Pull Request、レビュー、コメント、コミットメッセージ、`summary`、`blocked_reason` を、平易な英語で書く。短い文にし、同じものはいつも同じ語で呼ぶ。文章の型ごとに、テンプレート (`CWK-AGENT-007`) に従う。`blocked_reason` を英語にするのは、cumin がそれをそのままコメントにするためである (`CWK-AGENT-013`)。

- 検証: `demo` Implementer の Pull Request の説明が、英語で、テンプレートの見出しをすべて持つ

---

## 受け入れテスト

| # | 場面 | 期待する結果 |
|---|---|---|
| 1 | どの role でも、cumin が Agent を起動する | 起動の前に状態ラベルが替わり (`CWK-AGENT-002`)、Issue と role ごとの Agent作業ディレクトリで、その role の token だけを持って起動し (`CWK-AGENT-003`、`CWK-AGENT-005`)、4つの部分の指示と、決められた事実を受け取る (`CWK-AGENT-006`、`CWK-AGENT-010`) |
| 2 | Host のユーザアカウントに MCP サーバか claude.ai のコネクタがある状態で、Agent を起動する | 起動の記録で見つかって実行が止まり、異常終了になる (`CWK-AGENT-009`、`CWK-AGENT-017`)。各ページのやり直しが1回起き、同じなら止まって通知が届く |
| 3 | Agent が `blocked` を返す | `blocked_reason` がコメントになり、やり直さずに止まる (`CWK-AGENT-013`) |
| 4 | Agent の実行が時間の上限を過ぎる | 打ち切られ、Agent が起動したコマンドも残らず (`CWK-AGENT-015`)、異常終了として各ページのやり直しが起きる (`CWK-AGENT-017`) |
| 5 | 対象のリポジトリのツール動作確認が失敗する | そのリポジトリの Agent起動だけが止まり、マージやほかのリポジトリの Agent起動は続く (`CWK-AGENT-018.3`) |
| 6 | マージされて実装Issueが閉じる | Agent作業ディレクトリが片付く。GitHub にないものがあれば残る (`CWK-AGENT-004`) |

## 未決事項

なし
