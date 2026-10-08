# cumin本体の要件

## 位置づけ

cumin本体は、Goで書くワークフローの基盤であり、Agentではない。決められたルールだけで動き、AIによる判断を含まない。

- 判断が要ることは、全てAgentかMaintainerが行う。要求の解釈、分割、実装、レビュー、riskの判断がこれに当たる。
- cumin本体が行う判定は、GitHub上の事実と、Agentが返したJSONを、決まった条件に照らすことだけである。同じ状態からは、いつも同じ動作になる。

この文書では、cumin本体を単にcuminと書く。

## Maintainer、Issue Owner、Operator

cuminに関わる人を、3つの名前で呼び分ける。1人が3つを兼ねてもよい。

| 名前 | 誰か | 何人か |
|---|---|---|
| Maintainer | 対象のリポジトリに write 以上 (write、maintain、admin) の権限を持つ、人のアカウント | 何人でも |
| Issue Owner | そのIssueに今付いている `cumin/status/ready` を付けたMaintainer | Issueごとに1人 |
| Operator | Hostでcuminを動かしている人 | Hostごとに1人 |

Maintainer:

- cuminが判定に使う。`cumin/status/ready` を付けて「進めてよい」と合図できるのも、Pull Requestを承認してmergeさせられるのも、状態ラベルを手で直せるのも、Maintainerである
- botのアカウントは、権限があってもMaintainerではない。cuminのGitHub AppはMaintainerにならないので、ReviewerのAppの承認は、Maintainerの承認に数えない
- adminだけに絞らないのは、リポジトリにadminを増やさずにMaintainerを置けるようにするためである
- 「write以上」だけにしないのは、cuminのGitHub Appもwriteの権限を持つためである
- GitHubの役割の Maintain だけを指す名前ではない。Write と Admin の人も、Maintainerである
- Maintainerの一覧は設定に持たない。GitHub上の権限から、その都度決める

Issue Owner:

- どの付け方が「今付いている `cumin/status/ready` を付けた」に当たるかは、[Issueのラベルと状態遷移](workflow/issue-states.md) の「状態ラベルを付けたアカウント」に従う
- Issueごとに決まる。要求Issueと、そのsub-issueで、違う人になりうる。sub-issueのIssue Ownerは、そのsub-issueに `cumin/status/ready` を付けた人である
- cuminは、Agentを起動するときに、Issue Ownerのログイン名を渡す ([Agentに共通の要件](agents/common.md))。Agentは、その人のコメントを指示として読む
- cuminがmergeの判断のためにレビューを依頼する相手は、実装IssueのIssue Ownerである。承認は、どのMaintainerのものでも数える
- 止まったIssueに、別のMaintainerが `cumin/status/ready` を付け直すと、Issue Ownerはその人に替わる

Operator:

- Host上のコマンド (`cumin run`、`cumin status`、`cumin quota allow`、`cumin stop`、`cumin setup`)、Hostの設定ファイル、Keychain、バイナリの入れ替えを受け持つ
- 利用枠は、HostのClaude Codeのアカウントのものである。「自分の作業のために利用枠を残す」のは、Operatorである
- リポジトリの権限とは関係がない。OperatorがMaintainerでないこともありうる

通知:

- 通知先は、Hostの設定で決まる。誰がそれを読むかは、その設定しだいである。そこで、文書では「通知する」とだけ書き、相手の名前を書かない

## 受け持つこと

| 受け持つこと | 内容 |
|---|---|
| GitHubの定期確認 | 対象のリポジトリのIssue、Pull Request、check、レビューを、決まった間隔で確かめる。作業中のIssueがないリポジトリは、長い間隔 (アイドルの間隔) で確かめる。作業中とは、そのリポジトリでAgentが動いているか、`cumin/status/ready`、`cumin/status/planning`、`cumin/status/accepting` の要求Issue、または `cumin/status/ready`、`cumin/status/implementing`、`cumin/status/checking`、`cumin/status/reviewing`、`cumin/status/merging` のsub-issueがあるか、前回の定期確認で何か動作をしたか失敗したことである。Maintainerの判断を待つIssueしかないリポジトリは作業中でないので、Maintainerの `cumin/status/ready` や承認には、アイドルの間隔のうちに気付く |
| 状態の管理 | `cumin/status/*` のラベルを付け替える。条件は [Issueのラベルと状態遷移](workflow/issue-states.md) に従う。実装Issueの状態とriskのラベルを、そのIssueを閉じるPull Requestにもコピーする |
| Agentの起動 | roleごとの指示、作業場所、GitHub Appのtokenを用意して、Agentを起動する。終了を待ち、結果のJSONを検証する |
| 事実の確認 | Agentが `done` を返したあと、完了したかどうかをGitHub上の事実で確かめる |
| merge | `risk/low` で、Reviewerが承認し、必須のcheckが通ったPull Requestをmergeする (「start the merge」)。`risk/medium` と `risk/high` は、Maintainerが承認したあとにmergeする (「start the merge」)。mergeの方法は設定で選べる (初期値はsquash)。mergeのあと、GitHubが実装Issueを閉じなければ、そのmergeの手順の中で1回だけ閉じる |
| フォローアップノート | Pull Requestがmergeされたら、その説明の `Follow-up` と、対応されなかった `(non-blocking)` の指摘を、フォローアップノートとして要求Issueに転記する。フォローアップノートは、要求Issueに付ける1つのコメントである。AIの判断は使わず、決まった形式から機械的に拾う |
| 通知 | 人の対応が要るとき、Discordのwebhookで知らせる |
| 利用枠の管理 | 使用率を読み、上限に達している間はAgentの起動を止める。weekly枠は週を通して配分し、5h枠はOperatorの分を時間帯ごとに残す |
| Operatorからの操作の受け付け | Host上のコマンドで、状態の表示と、利用枠の使い切りの許可を受け付ける |

cuminの動作ごとのきっかけと、動く前に確かめることは、[Issueのラベルと状態遷移](workflow/issue-states.md) の表に書いてある。この文書では繰り返さない。

## 受け持たないこと

- コードを書かない。レビューしない。要求を解釈しない。riskを判断しない
- `risk/medium` と `risk/high` のPull Requestを、Maintainerの承認なしにmergeしない
- `cumin/type/requirement` と `cumin/status/ready` を付けない。この2つはMaintainerの意思表示である。例外として、着手のときに `cumin/status/ready` を外す
- mainに直接pushしない。強制pushしない。mainへの変更は、Pull Requestのmergeだけで行う
- 人の認証情報 (Hostにある、Operatorの `gh` のログインなど) と、リポジトリの管理者の権限 (Administration) を使わない。rulesetなどの、管理者の権限が要る準備は、管理者が自分の `gh` でスクリプトを実行して行う
- 自分のmergeの手順の外で、Issueを閉じない。実装Issueは、Pull RequestのmergeによってGitHubが閉じる。GitHubが閉じなかったときだけ、cuminがmergeの直後に1回だけ閉じる。あとの定期確認では閉じないので、Maintainerが開き直した実装Issueは開いたままになる。要求Issueは、Maintainerが閉じる

## 動かし方

- Hostの上で、常駐プログラムとして動く。落ちたらlaunchdが再起動する
- 人が起動するのを待たない。自分からGitHubを確かめて、仕事を取りに行く
- ログは、機械で読める形で標準出力に出す
- 将来は、ハートビートを出して、外部のマシンから死活を監視できるようにする

Operatorが使うコマンド:

| コマンド | 内容 |
|---|---|
| `cumin run` | 常駐して動く。launchdから起動する |
| `cumin status` | 今の状態を表示する。実行中のAgent (`cumin/status/planning`、`cumin/status/accepting`、`cumin/status/implementing`、`cumin/status/reviewing` のIssue)、Maintainerの対応を待っているIssue、両方の枠の最新の使用率とそれを読んだ時刻、今の上限、Agentの起動を止めているかどうかとその原因の枠、実行が終わるのを待って止まる途中かどうか、全部を読めないIssueとその超えた上限 (「全部を読めないIssue」) |
| `cumin quota allow` | 今の5h枠を使い切ってよいと許可する。許可は、その5h枠がリセットされるまで有効。weekly枠には効かない |
| `cumin stop --after-current-runs` | 実行中のAgentの実行が終わるのを待ってから、cuminを止める。新しい依頼は始めない。Agentの要らない動作は、止まるまで続ける |
| `cumin --version` | cuminの版を表示する |
| `cumin setup github-apps` | 導入のときに、Hostで実行する。roleごとのGitHub Appを登録し、秘密鍵をKeychainに入れる。もう一度実行すると、足りないroleだけを登録する |

## 利用枠の守り方

先に尽きるのは、weekly枠である。5h枠を毎回80〜90%まで使うと、weekly枠は数日で尽きる。そのあとcuminは何日も止まり、Operatorの作業に使う分も残らない。そこで、weekly枠は週を通して均等に使うように配分し、5h枠はOperatorの分を時間帯ごとに残すために使う。

- weekly枠の使用率が、ペースの上限以上の間、cuminはAgentの起動を止める。着手だけでなく、実行中のIssueの続きの依頼も止める。動いているAgentは止めない ([Issueのラベルと状態遷移](workflow/issue-states.md) の「cumin自身の状態 (Agentの起動)」)
- ペースの上限は、目標 × min(1, (経過時間 + 前倒し) ÷ 7日) である
  - 経過時間は、weekly枠の始まりからの時間である。始まりは、weekly枠のリセット時刻の7日前とする
  - リセット時刻は、使用率を読むたびにClaude Codeから受け取る。リセットの曜日や時刻を、設定には持たない
  - 目標は、週の終わりまでに使ってよい使用率である。前倒しは、週の始めにも少し先の分まで使えるようにする幅である。どちらも設定で変えられる
- 5h枠の使用率が、今の時刻の時間帯のしきい値以上の間、cuminはAgentの起動を止める。どの時間帯にも入らない時刻には、初期のしきい値を使う
- 使い切りを許可できるのは、5h枠だけである。Operatorが `cumin quota allow` で許可すると、その5h枠がリセットされるまで、5h枠のしきい値を100%とする
- weekly枠のペースの上限は、どの方法でも上げられない。`cumin quota allow` でも上がらない。weekly枠を使い切ると、Operatorが何日も使えなくなるためである。これはClaude Codeでも、Codexでも同じである
- 5h枠の使い切りが許可されていても、weekly枠の使用率がペースの上限以上なら、Agentの起動を止める

## 止めたとき、スリープしたとき

v0.1では、次のように割り切る。Hostが常時動くMac miniになれば、ほとんど起きない。

- cuminを途中で止めても、Hostがスリープしても、作業の状態はGitHubにあるので失われない
- スリープから戻ると、cuminも実行中のAgentも続きから動く。ただし、次のことが起こりうる。通信の途中だった要求が失敗する。Agentに渡したGitHub Appのtokenが、時間切れになっている (tokenは発行から1時間で失効する)。時間で区切る打ち切りが、戻った直後に働く
- どれが起きても、Agentの異常終了として扱う。同じ依頼を1回だけやり直し、それでも駄目なら通知する
- cuminを止めたときに作業中の状態のまま残ったIssueは、cuminを起動し直せば、最初の定期確認が、GitHub上の事実から続きを決める ([Issueのラベルと状態遷移](workflow/issue-states.md) の「原則」)。取り消されたAgentの実行は、1回だけ依頼し直す
- `cumin stop --after-current-runs` で止めたときは、実行中のAgentの実行が終わるのを待ってから止まる。実行の終わりに続く動作のうち、Agentを起動しないもの (ラベルの付け替え、コメント、通知、merge) は済ませる。Agentへの次の依頼 (指摘の修正、依頼し直しなど) は始めず、そのIssueは今の状態のまま残す。どの状態でも、次の定期確認が事実から次の動作を決めるので、cuminを起動し直せば続きから進む。止める予約は、次の起動には残らない。SIGTERMは、今までどおりすぐに止める

## GitHubの呼び出しの失敗

GitHubへの呼び出しは、すぐに直る理由で失敗することがある。cuminは、そのような失敗を自分でやり直し、Issueを止めない。

- 一時的な失敗は、次の4つである。ネットワークの誤り (タイムアウト、接続の切断など)。5xxの応答。二次のレート制限。一次のレート制限を使い切ったこと
- それ以外の失敗 (レート制限ではない4xx、見つからないものなど) は、やり直さない。今までどおり、各行の「うまくいかないとき」に従う
- レート制限ではない一時的な失敗 (ネットワークの誤りと5xxの応答) は、読み取りに限り、数秒あけて3回までやり直す。書き込みはやり直さない。答えが届かなかった書き込みをやり直すと、同じコメントなどを二重に書くおそれがあるためである
- レート制限は、GitHubが返す時刻まで待つ。一次のレート制限はリセットの時刻まで、二次のレート制限は `retry-after` のぶん、それがなければ1分である。待つ間、cuminはそのinstallationでGitHubを呼ばない。呼び出しの中では待たない。定期確認が、その時刻のあとにやり直す。待つ間も、ほかのリポジトリの定期確認や、ほかの手順は進める
- Agentの実行のあとで、やり直しても一時的な失敗で終わったときは、cuminは何も持っておかない。Issueは今の状態のまま残り、次の定期確認が、GitHub上の事実から同じ動作を決める。すでに済んだ書き込み (ラベル、コメント、merge) は読み直しで見えるので、二重にはしない。mergeの答えが届かなかったときも、Issueは `cumin/status/merging` のまま残るので、次の定期確認が、Pull Requestがmerge済みかを読んで続ける
- 数 (3回、数秒、1分、5分) は、設定にせず固定の値にする
- 一次のレート制限を使い切っている間は、定期確認が続けて失敗するので、「同じリポジトリの定期確認が続けて失敗した」の通知が出る。通知を読んだ人は、それでレート制限に気付く

## 全部を読めないIssue

cuminは、1つのIssueについて読む数に上限を持つ。上限を超えたIssueは、全部を読めないIssueである。

- sub-issueは、12件ずつ読む。12件は、1回の分割の上限と同じ数である ([要求Issueの大きさの基準](policies/requirement-sizing.md))。続きがあるIssueだけ、次のページを読む。3ページ、36件までである。sub-issueが12件以下のIssueしかない定期確認は、読む量が増えない
- 分割し直した要求Issueは、前の分割の閉じたsub-issueを付けたままにできる。36件までは、定期確認が全部を読む
- sub-issueが36件を超えたIssueは、全部を読めないIssueである。ラベル、blocked by のIssue、結び付いたPull Requestが、cuminの読む数を超えたIssueも同じである
- sub-issueの1つが全部を読めないIssueなら、その要求Issueも、全部を読めないIssueとして扱う
- 全部を読めないIssueについて、cuminは何も決めない。ラベルも替えない。一部だけを読んだ事実から決めると、間違った動作になるためである
- 同じ定期確認で、ほかのIssueは今までどおり進める。1つのIssueが、リポジトリ全体の定期確認を止めない
- cuminは、全部を読めないIssueを1回だけ通知する。通知には、Issueと、超えた上限を入れる。次に知らせるのは、そのIssueを全部読めた定期確認のあとである。cuminを再起動すると、もう一度知らせることがある
- 数 (12件、3ページ) は、設定にせず固定の値にする

## 状態の持ち方

- 作業の状態は、GitHubに置く。Issue、ラベル、Pull Request、レビュー、コメントが、状態の全てである
- cuminが手元に持つのは、失っても作業をやり直せるものだけにする。Agentのセッションの番号、checkの修正を依頼した回数、枠ごとの最新の使用率とリセット時刻とそれを読んだ時刻、使い切りの許可、止める予約、Agentに依頼し直した回数がこれに当たる
- cuminは、cuminを見守る道具 (メニューバーの表示など) のための監視用のファイルも、手元に書く。定期確認のたびと、Agentの実行が終わるたびに、書き直す。cuminはこのファイルを読まず、ここからは何も決めない。cumin自身の状態ではなく、見せるための写しなので、失っても何も失わない。次に書くときに、同じ内容がもう一度できる
- レビューのラウンド数は、手元に持たずに、Pull Requestに出ている `cumin-reviewer` のレビューの数から数える
- cuminが再起動しても、GitHubを確かめ直せば、続きから動ける。作業中の状態のまま残ったIssueも、次の定期確認が事実から続きを決める。Maintainerが `cumin/status/ready` を付け直す必要はない

## Agentの起動

Agentの起動について、cuminが守ること。Agentの側の要件は [Agentに共通の要件](agents/common.md) にある。

- 着手のときは、Agentを起動する前にラベルを付け替える。同じIssueを二重に依頼しない
- 作業場所は、`git worktree` でIssueごとに用意する。Issueが閉じたら片付ける
- GitHub Appのtokenは、依頼のたびに、そのroleのAppの分だけを発行して渡す。人の認証情報を、Agentに渡さない
- Agentが、ユーザアカウントのグローバルな指示を読まないようにして起動する。Claude Codeでは `--setting-sources project` を付け、自動メモリも切る
- Agentの環境には、そのroleのtokenだけを入れる。Hostのユーザアカウントにあるgitとghの認証の設定は、Agentに参照させない
- Agentの起動の記録に、作業場所の外にある指示やメモリ、ユーザアカウントのplugin、MCPサーバが現れたら、異常終了として扱う。Claude Codeでは、実行の最初に出る `init` のイベントで確かめられる
- Agentの起動の記録に、cuminがそのroleのために書き出したskillが全て載っていることを確かめる。1つでも欠けていたら異常終了として扱う。skillが届かないと、Agentは決まった形式の文章を、テンプレートではなく記憶から書くことになる
- Agentの実行には、時間の上限を設ける。上限を超えたら打ち切り、異常終了として扱う
- Agentは、ツールの使用を全て許可するモードで起動する。headlessの実行では、許可を尋ねられても答える人がいないためである。roleごとの制限は、GitHub Appの権限とrulesetで行う
- 結果のJSONは、cuminの側でも検証する。形式に合わなければ、異常終了として扱う
- 異常終了したら、同じ依頼を1回だけやり直す。それでも駄目なら、`cumin/status/awaiting-decision` に替えて通知する
- Agentが `blocked` を返したら、`blocked_reason` をIssueにコメントとして投稿してから、通知する。Maintainerは、GitHubの上で理由を読める
- roleごとに使うCLI (Claude Code、Codexなど) は、設定で選べるようにする。CLIごとの違いは、cuminの中のCLIごとの接続部分に閉じ込める

## フォローアップノート

Implementerが範囲の外だと判断した作業と、Reviewerの提案のうち対応されなかったものは、mergeされるとPull Requestの中に埋もれる。cuminは、これをフォローアップノートに転記する。フォローアップノートは、cuminが要求Issueに付けるコメントである。mergeされたPull Requestごとに、1つ付く。Maintainerは、受け入れのときに要求Issueを開けば、その要求で残った作業を見渡せる。

- きっかけは、実装Issueを閉じるPull Requestがmergeされたことである。cuminがmergeしたときも、Maintainerがmergeしたときも同じに扱う
- 拾うものは2つある。Pull Requestの説明の `Follow-up` の節の文章と、対応されなかった `(non-blocking)` の指摘である
- 対応されなかった指摘とは、`cumin-reviewer` の `(non-blocking)` の指摘のうち、`Fixed` か `Answer` で始まる返答が付いていないものである。ラベルが `praise` と `note` の指摘は拾わない
- 拾うものが何もなければ、フォローアップノートを書かない
- フォローアップノートを書くのは、要求Issueが開いている間だけである。閉じた要求Issueには書かない
- sub-issueが全て閉じたときは、フォローアップノートを、受け入れの確認の依頼 (「request the acceptance check」) より先に書く。Plannerが確認を始めるときにも、Maintainerが通知を見て来たときにも、フォローアップノートがそろっている
- 1つのPull Requestについて、フォローアップノートは1つだけにする。コメントに目印を埋め込み、cuminが再起動しても二重に書かない
- 形式は [follow-up-note.md](../../../templates/follow-up-note.md) に従う
- フォローアップノートは記録である。Issueにはしない。Maintainerは受け入れのときに一覧を見て、やりたいものを新しい要求Issueに書く。そこからは通常のフローに乗り、Plannerが実装Issueに分割する

## 通知

通知するのは、人の対応が要るときと、cuminが止まったときだけである。通知は「見に来てほしい」と伝えるだけで、やりとりはIssueとPull Requestで行う。

| 知らせるとき | 遷移の名前 |
|---|---|
| 分割結果の確認が必要 | 「ask for the plan review」 |
| 残りのsub-issueの確認が必要 | 「ask about the remaining sub-issues」 |
| 要求が受け入れ可能になった | 「ask for the acceptance」 |
| mergeの判断が必要 | 「ask for the merge decision」 |
| Agentが先に進めない。指摘が残った。mergeできない、またはmergeのあとに実装Issueを閉じられない | 「stop the implementation」、「stop for failed checks」、「stop the merge」、「stop at the round limit」、「stop the review」、「stop the split」、「stop the acceptance check」 |
| 利用枠の使用率が上限に達した、または使用率を読み取れなかったので、Agentの起動を止めた | 「stop agent starts」 |
| Maintainerが動かなければ何も進まない (進められるIssueがなく、動いているAgentもなく、check待ちのIssueもない) | 「tell that cumin waits」 |
| Maintainerでないアカウントが `cumin/status/ready` を付けたので、着手しなかった | 「request the split」、「request the implementation」 (Maintainerのready) |
| 同じリポジトリの定期確認が、同じ理由で続けて失敗した (3回)。次に知らせるのは、その間に定期確認が成功したあとである | — |
| 全部を読めないIssueがある (「全部を読めないIssue」)。Issueごとに1回。次に知らせるのは、そのIssueを全部読めたあとである | — |

通知には、対象のIssueかPull Requestへのリンクを入れる。通知の手段は、将来差し替えられるようにする。

## 設定

設定はTOMLのファイルに書く。値をコードに埋め込まない。

秘密の値 (GitHub Appの秘密鍵、Discordのwebhookのアドレス) は、設定ファイルにもリポジトリにも書かない。Hostの macOS のKeychainに置き、cuminが起動時に読む。複数のHostから同じ値を使うようになったら、Secrets Managerに移す。

設定には、Hostに属するものと、リポジトリごとに変えてよいものがある。

- Hostに属する設定は、Hostの設定ファイルにだけ書ける。利用枠はアカウントのものであり、作業場所や秘密の値はHostのものなので、リポジトリからは変えられない
- リポジトリごとに変えてよい設定は、Hostの設定ファイルに書いた値を、対象のリポジトリの `.cumin/` で上書きできる。優先順位は、初期値、Hostの設定ファイル、リポジトリの `.cumin/` の順に強くなる
- 保護されたパスと優先度のラベルは、リポジトリの `.cumin/config.toml` だけで決める。保護されたパスは、強制するのがGitHub Actionsのcheckであり、Hostの設定ファイルはそこから見えないためである。優先度のラベルは、ラベルがリポジトリにあり、足りないラベルを作るスクリプトがリポジトリのファイルを読むためである

| 設定 | 内容 | 初期値 | リポジトリで上書き |
|---|---|---|---|
| 対象のリポジトリ | cuminが確かめるリポジトリの一覧 | なし | できない |
| 定期確認の間隔 | GitHubを確かめる間隔 | 60秒 | できない |
| アイドルの間隔 | 作業中のIssueがないリポジトリを確かめる間隔。対象のリポジトリが増えても、GitHub GraphQLのポイントの枠に収めるためである。定期確認の間隔より短くできない | 5分 | できない |
| リポジトリごとに同時に進めるIssueの数 | 1つのリポジトリで、同時に進めるIssueの数の上限。数えるのは、`cumin/status/planning` と `cumin/status/accepting` の要求Issueと、`cumin/status/implementing`、`cumin/status/checking`、`cumin/status/reviewing`、`cumin/status/merging` の開いている実装Issueである。`cumin/status/implementing` の要求Issue (「mark the requirement as in work」) は、Agentが動いていないので数えない。人の番を待っているIssueも、`cumin/status/ready` が付いているIssueも数えない。違うリポジトリのIssueは、並行して進めてよい | 1 | できない |
| 5h枠のしきい値 | 5h枠の使用率がこれ以上なら、Agentの起動を止める。時間帯ごとに指定できる。どの時間帯にも入らない時刻には、初期のしきい値を使う | 85% | できない |
| weekly枠の目標 | ペースの上限の式の目標。weekly枠に時間帯はない | 85% | できない |
| weekly枠の前倒し | ペースの上限の式で、経過時間に足す時間 | 1日 | できない |
| 作業場所 | `git worktree` を置くディレクトリ | なし | できない |
| Agentの実行時間の上限 | roleごとに指定できる。GitHub Appのtokenが発行から1時間で失効し、期限を延ばせないので、55分を超える値は指定できない | 50分 | できない |
| roleとGitHub Appの対応 | roleごとのAppのClient ID。秘密鍵がHostにあるので、Hostの設定に書く。リポジトリの持ち主 (Organization) ごとに指定できる | なし | できない |
| レビューのラウンドの上限 | これを超えて指摘が残ったら、Maintainerに回す | 3 | できる |
| checkの修正を依頼する回数の上限 | これを超えたら、Maintainerに回す | 3 | できる |
| checkの待ち時間 | 必須のcheckが全て結果を返すのを待つ時間。過ぎたら、Maintainerに回す ([Issueのラベルと状態遷移](workflow/issue-states.md) の「stop for missing checks」)。CIの長さはリポジトリごとに違うので、リポジトリで上書きできる | 60分 | できる |
| roleごとのCLI | roleごとに、どのCLIとモデルでAgentを動かすか | Claude Code | できる |
| mergeの方法 | cuminがPull Requestをmergeするときの方法。squash、merge、rebaseのどれか | squash | できる |
| 優先度のラベル | 着手の順番を決めるラベルの一覧。優先度の高い順に書く ([Issueのラベルと状態遷移](workflow/issue-states.md) の「着手の順番」)。設定に書いたラベルはOrganizationのものなので、cuminは作らず、変えない。足りないラベルは、リポジトリの準備のスクリプトが、実行した人に尋ねてから作る。設定に書かなければ初期値のラベルを使い、足りないものをcuminが作る | `cumin/priority/P0`、`cumin/priority/P1`、`cumin/priority/P2`、`cumin/priority/P3` | リポジトリだけで決める |
| 保護されたパス | Agentに変更させないパスの一覧 | `.cumin/`、`CLAUDE.md`、`AGENTS.md`、`.claude/` | リポジトリだけで決める |
| riskの基準 | riskの基準を書いたMarkdownの文章。cuminは中身を解釈せず、PlannerとReviewerへの指示にそのまま入れる | `disciplines/software-engineering/risk-criteria.md` | できる |

保護されたパスは、`.cumin/config.toml` の `protected_paths` に、文字列の配列で書く。照合の決まりは、`.gitignore` の一部と同じである。cuminは、この一覧を読み、照合の決まりとともに、起動の依頼のデータとしてAgentに渡す ([Agentに共通の要件](agents/common.md) の「起動の依頼の事実」)。

- 末尾のほかに `/` を含まない項目 (例: `CLAUDE.md`、`.claude/`) は、どの階層にあっても当たる
- 先頭が `/` の項目、または途中に `/` を含む項目は、リポジトリの直下から数えた位置にだけ当たる
- 末尾が `/` の項目 (例: `.cumin/`) は、ディレクトリを表し、その下の全てに当たる
- ワイルドカードは使えない
- 大文字と小文字は区別しない。Hostの macOS のファイルシステムが区別しないので、`claude.md` という名前のファイルも、Agentには `CLAUDE.md` として読まれるためである

どの階層でも当たるようにするのは、Claude Codeが、サブディレクトリの `CLAUDE.md` も読むためである。リポジトリの直下だけを守ると、Implementerがサブディレクトリに `CLAUDE.md` を足して、あとに続くAgentへの指示を変えられる。

riskの基準は、TOMLの値ではなく、Markdownのファイルで上書きする。Hostでは、設定ファイルと同じディレクトリの `risk-criteria.md` に書く。リポジトリでは、`.cumin/risk-criteria.md` に書く。ファイルがあれば、その内容が、それより弱い段の基準を丸ごと置き換える。優先順位は他の設定と同じで、初期値、Hostのファイル、リポジトリのファイルの順に強くなる。

cuminは、リポジトリの `.cumin/` を、Pull Requestのブランチではなくmainから読む。`.cumin/` の変更は常に `risk/high` なので、Maintainerが承認したものだけが効く。

## GitHub上の身元

GitHub上では `cumin-core` として振る舞う。持っている権限は、コードの読み書き (mergeに要る)、Pull Requestの読み書き、Issueの読み書きである。mainを更新できるのは、rulesetによって、リポジトリの管理者と `cumin-core` だけに制限する。登録の手順は [GitHub Appの登録手順](../development/github-app-setup.md) にある。

## 上位要件のテスト

| # | 場面 | 期待する結果 |
|---|---|---|
| 1 | `cumin/status/ready` の実装Issueを1つ置き、定期確認を2回以上またぐ | Implementerへの依頼は1回だけ行われる |
| 2 | blocked by のIssueが開いている実装Issueに `cumin/status/ready` を付ける | 着手しない。blocked by のIssueが閉じたら着手する |
| 3 | `risk/low` で承認され、checkの通ったPull Requestがある | cuminがmergeし、実装Issueが閉じる |
| 4 | `risk/medium` で承認され、checkの通ったPull Requestがある | mergeしない。`cumin/status/awaiting-merge-decision` に替えて、通知する |
| 5 | Agentが形式に合わない結果を返す | 同じ依頼を1回だけやり直す。それでも合わなければ `cumin/status/awaiting-decision` に替えて通知する |
| 6 | 5h枠の使用率が、今の時間帯のしきい値に達する | Agentの起動を止めて、1回だけ通知する。動いているAgentは止めない。その実行が終わったあとの続きの依頼 (レビュー、修正など) は出さず、Issueは今の状態のまま残る。`cumin quota allow` で再開する。5h枠のリセット時刻を過ぎるか、しきい値の高い時間帯に入ると、自動で再開する |
| 7 | 要求Issueのsub-issueが全て閉じる | Plannerに、受け入れの確認が1回だけ依頼される。確認のコメントが付いたあとで、要求Issueを `cumin/status/awaiting-acceptance` に替えて、通知する。表にFailがあっても、同じ動作になる |
| 8 | cuminを止めて、起動し直す | GitHubを確かめ直して動き始める。同じIssueを二重に依頼しない |
| 9 | 進められるIssueがなくなり、動いているAgentもいない | 1回だけ通知する。cuminが何か動作をするまで、同じ通知を繰り返さない |
| 10 | `Follow-up` に文章があり、対応されなかった `(non-blocking)` の指摘が1つあるPull Requestをmergeする | 要求Issueに、決められた形式のフォローアップノートが1つ付く。cuminを再起動しても、同じフォローアップノートは増えない |
| 11 | `Follow-up` が None で、`(non-blocking)` の指摘が全て `Fixed` になったPull Requestをmergeする | 要求Issueにフォローアップノートは付かない |
| 12 | 要求Issueのsub-issueの一部にだけ `cumin/status/ready` を付け、それらが全て閉じる | 要求Issueを `cumin/status/awaiting-plan-review` に替えて、1回だけ通知する。残りのsub-issueに `cumin/status/ready` を付けると、要求Issueが `cumin/status/implementing` に戻る |
| 13 | `cumin/status/ready` のsub-issueが残っている要求Issueを見直し、Plannerが新しいsub-issueを足す | Maintainerが新しく `cumin/status/ready` を付けるまで、要求Issueは `cumin/status/awaiting-plan-review` のままである |
| 14 | cuminが実装Issueのラベルを付け替える。Maintainerが実装Issueのriskを変える。MaintainerがPull Requestの側のラベルを変える | どの場合も、次の定期確認のあとで、Pull Requestの `cumin/status/*` と `risk/*` が、実装Issueと同じになる。cuminの判定は、Pull Requestのラベルに左右されない |
| 15 | weekly枠の使用率が変わらないまま、週の始め、中ごろ、終わりに、着手できるIssueがある | 週の始めはAgentの起動を止めて、1回だけ通知する。経過時間とともにペースの上限が上がり、使用率を上回ったあとの定期確認で、自動で再開する。上限は目標を超えない |
| 16 | weekly枠の使用率がペースの上限に達していて、Operatorが `cumin quota allow` を実行する | Agentの起動を再開しない |
| 17 | Agentの起動の前の確認で、使用率を読み取れない | 起動せずに、1回だけ通知する |
| 18 | `cumin/status/awaiting-merge-decision` の実装IssueのPull Requestを、Maintainerが今の先頭のコミットでGitHubのレビューにより承認する | cuminがmergeし、実装Issueが閉じる。古いコミットへの承認、botの承認、writeの権限のないアカウントの承認、あとから `REQUEST_CHANGES` で覆された承認では、mergeしない |
| 19 | cuminがmergeしたあと、GitHubが実装Issueを閉じない | cuminが1回だけ閉じる。Maintainerがそれを開き直しても、あとの定期確認では閉じない |
| 20 | 着手できるIssueが2つあり、番号の大きいほうに、より高い優先度のラベルが付いている | 優先度の高いほうから着手する。同じ優先度なら、番号の小さいほうから着手する。優先度のラベルがないIssueは、最後に着手する。設定でラベルの名前を変えると、その名前で順番が決まる |
| 21 | 必須のcheckを待つ実装Issueが1つだけあり、動いているAgentもいない | 待ち状態の通知 (「tell that cumin waits」) を出さない。checkが終わって進み、Maintainerの対応だけが残ったときに、1回だけ通知する |
| 22 | `cumin/status/awaiting-merge-decision` の実装IssueのPull Requestに、Maintainerが今の先頭のコミットで `REQUEST_CHANGES` を出す | cuminが実装Issueを `cumin/status/implementing` に替え、Implementerの直前のセッションで直させる。直したあと、check、Reviewerのレビューを経て、もう一度Maintainerの判断を待つ。Implementerがコミットせずに答えたときも、もう一度Maintainerの判断を待ち、同じレビューで2回目の差し戻しはしない。botや、writeの権限のないアカウントの `REQUEST_CHANGES` では、何もしない |
| 23 | Maintainerでないアカウントが、Issueに `cumin/status/ready` を付ける | 着手しない。ラベルは替えない。1回だけ通知する。Maintainerが `cumin/status/ready` を付け直すと着手する |
| 24 | `cumin/status/checking` または `cumin/status/awaiting-merge-decision` の実装IssueのPull Requestが、既定のブランチと衝突する | cuminが実装Issueを `cumin/status/implementing` に替え、Implementerに衝突の解消を1回だけ依頼する。GitHubがまだ計算している (`UNKNOWN`) 間は依頼しない。checkの修正を依頼した回数は増えない |
| 25 | `cumin/status/checking` の実装Issueで、必須のcheckが、checkの待ち時間を過ぎても先頭のコミットで結果を返さない | `cumin/status/awaiting-decision` に替え、先頭のコミット、結果を返していない必須のcheck、待った時間を添えて、1回だけ通知する。待ち時間の内に結果が返れば、「request the review」、「request a check fix」、「stop for failed checks」のどれかで進む。開いているPull Requestがなくなったときも、待ち時間を過ぎたら、そのことを添えて1回だけ通知する |
| 26 | 作業中のIssueがないリポジトリと、作業中のIssueがあるリポジトリを、同時に対象にする | 作業中のリポジトリは定期確認の間隔で、作業中でないリポジトリはアイドルの間隔で確かめる。作業中でないリポジトリでMaintainerが `cumin/status/ready` を付けると、アイドルの間隔のうちに着手する |
| 27 | Agentの実行が終わったあと、GitHubの読み取りが1回だけネットワークの誤りで失敗する | 数秒あけた読み取りで成功し、そのまま次の手順に進む |
| 28 | Agentの実行が終わったあと、GitHubの読み取りが、やり直しても一時的な失敗で終わる | Issueはラベルを保つ。あとの定期確認で読み直し、成功したら、失敗しなかったときと同じ動作をする。書き込みは二重にならない。cuminを再起動しても、同じ結果になる |
| 29 | 一次のレート制限を使い切る | リセットの時刻まで、cuminはGitHubを呼ばない。リセットのあとの定期確認で、続きから進む |
| 30 | triageの権限のアカウントが、Issueに `cumin/status/planning` や `cumin/status/merging` などの状態ラベルを付ける | cuminは、Agentを起動せず、mergeせず、ラベルも替えない。ログに1回だけ残し、1回だけ通知する。`cumin-core` かMaintainerが付けた状態ラベルでは、今までどおり動く |
| 31 | 利用枠が上限に達している間に、実装Issueの必須のcheckが通る | Reviewerを起動しない。実装Issueは `cumin/status/checking` のまま残り、ラベルは替わらない。上限のあとの定期確認で、レビューが1回だけ依頼される |
| 32 | 利用枠が上限に達している間に、Reviewerが `REQUEST_CHANGES` を出して終わる | Implementerを起動しない。実装Issueは `cumin/status/reviewing` のまま残る。上限のあとの定期確認で、指摘の修正が1回だけ依頼される |
| 33 | 利用枠が上限に達している間に、承認されたPull Requestが `cumin/status/merging` にある | mergeする。Agentを起動しない動作は、上限に関係なく進む |
| 34 | 利用枠だけで待っているIssueがあり、動いているAgentがいない | 「待ち状態になった」の通知 (「tell that cumin waits」) を出さない。利用枠の通知 (「stop agent starts」) は、1つの上限につき1回のままである |
| 35 | sub-issueが20件ある要求Issue (閉じたものが8件、開いているものが12件) がある | 定期確認は失敗しない。20件を全部読み、今までどおり動く |
| 36 | sub-issueが36件を超える要求Issueと、`cumin/status/ready` の付いた別の実装Issueがある | 定期確認は失敗しない。別の実装Issueには着手する。36件を超える要求Issueとそのsub-issueには、何もしない。ラベルも替えない |
| 37 | 全部を読めないIssueがあるまま、定期確認を3回行う | 通知は1回だけである。通知に、Issueと超えた上限がある。`cumin status` が、そのIssueと上限を表示する |
| 38 | 全部を読めなかったIssueのsub-issueを減らして、36件以下にする | 次の定期確認から、そのIssueも今までどおり動く |
| 39 | `cumin/type/owner-task` のsub-issueだけが開いていて、要求Issueが `cumin/status/awaiting-plan-review` である。Maintainerがそのsub-issueを閉じる | 次の定期確認で、要求Issueが `cumin/status/accepting` に替わり、受け入れの確認が1回だけ依頼される。Maintainerはラベルを替えない |
