# 定期確認の設計

- 状態: Approved
- 要件: [Issueのラベルと状態遷移](../requirements/workflow/issue-states.md)、[cumin本体の要件](../requirements/cumin-core.md) の「GitHubの定期確認」「状態の管理」「Agentの起動」「設定」
- 事実の出どころ: [調査・実測で確定した制約](../evidence/measured-constraints.md) の行の番号 (「実測 N」と書く) か、公式ドキュメントのページの名前で示す。

定期確認の1回分 (GitHubを読み、判定し、着手し、実行の終わりを判定する) の設計を書く。issue-states.md の行 (R1〜R7、I1〜I11) を実現する決定は、この文書に足す。プログラム全体にまたがる決定 (Hostのファイル、Keychain、launchd、テストの2層、GitHubクライアント、riskの基準の受け渡し、起動前の使用率の確認) は [cumin本体の設計メモ](cumin-core.md) にある。

## 範囲

扱うこと:

- 定期確認と、そのきっかけで動く判定と動作の設計。`internal/workflow` が受け持つ範囲に当たる。

扱わないこと:

- 何をするか。要件文書にあり、ここでは繰り返さない。
- GitHubクライアントの認証、問い合わせの上限、REST と GraphQL の使い分け。[cumin本体の設計メモ](cumin-core.md) の「GitHubクライアント」にある。
- 1回のAgentの実行 (作業場所、CLIの起動、環境、時間の上限)。[Agentの実行の設計](agent-run.md) にある。

## 設計

### 定期確認の間隔

![定期確認の間隔](poll-interval.svg)

図の元ファイル: [poll-interval.puml](poll-interval.puml)

- cuminは `poll_interval` ごとに、対象のリポジトリを順に見る。作業中のリポジトリは毎回確かめる。作業中でないリポジトリは、前回の定期確認から `idle_poll_interval` が過ぎるまで飛ばす。飛ばすときは、GitHubを1回も呼ばない (cumin本体の要件の「GitHubの定期確認」、テスト26)。
- 作業中とは、次のどれかである。
  - そのリポジトリの定期確認を、まだ1回もしていない。
  - 前回の定期確認が失敗した。動作の1つが失敗したときも含む。
  - 前回の定期確認で、何か動作をした。Ownerの承認とOwnerのレビューの候補 (I12、I13) は、mergeか差し戻しまで進んだときだけ動作に数える。候補は、Ownerが動くまで毎回の判定に出るためである。
  - そのリポジトリでAgentの実行が進んでいる。または、前回の定期確認のあとに実行が終わった。
  - 前回のスナップショットに、`cumin/status/ready` か `cumin/status/planning` の開いている要求Issueがある。
  - 前回のスナップショットに、`cumin/status/ready`、`cumin/status/implementing`、`cumin/status/checking`、`cumin/status/reviewing` の開いているsub-issueがある。
- 判定は `internal/workflow` の純粋関数である。`Snapshot.HasIssueInWork` がスナップショットからIssueを見て、`RepositoryInWork` が前回の定期確認の結果と実行の有無から作業中かを返し、`PollIsDue` が前回からの時間と2つの間隔から、今回確かめるかを返す。時計を読むのは `Service.Poll` である。
- 前回の定期確認の時刻と結果は、リポジトリごとにメモリに持つ。GitHub上の事実ではないが、失っても作業を失わない。cuminが起動し直すと、全てのリポジトリを1回確かめるだけである。
- 作業中でないリポジトリが気付く速さ。Ownerが `cumin/status/ready` を付ける、Pull Requestを承認する、レビューで差し戻す、のどれにも、最長で `idle_poll_interval` (初期値は5分) のうちに気付く。気付いた定期確認は動作をするか、作業中のIssueを読むので、次の定期確認は `poll_interval` のあとに来る。
- 実行が終わると、ラベルが作業中のものでなくても、次の `poll_interval` で確かめる。mergeの手順は、実装Issueのラベルを作業中のものにしないまま進むので、実行の終わりを別に覚える。受け入れの確認 (R4) の間は、要求Issueが `cumin/status/accepting` なので、作業中のIssueとして読む。
- 時間の比べ方。`poll_interval` の刻みは、わずかに早く来ることがある。前回からの時間が `idle_poll_interval` に `poll_interval` の半分だけ足りなくても、確かめる。足りないからと次の刻みまで待つと、5分のはずが6分になるためである。`idle_poll_interval` が `poll_interval` の倍数でないときは、いちばん近い刻みで確かめる。
- リポジトリは、それぞれ別に判定する。作業中でないリポジトリを飛ばしても、他のリポジトリの定期確認は `poll_interval` のままである。
- 実行を待ってから止める間 ([cumin本体の設計メモ](cumin-core.md) の「実行を待ってから止める」) は、どのリポジトリも飛ばさない。最後の定期確認が全てのリポジトリを読む、という止め方を変えないためである。
- フォローアップノートを書けなかった定期確認 (コメントの読み取りの失敗) は、失敗に数えない。動作も作業中のIssueもなければ、次に試すのは `idle_poll_interval` のあとである。受け入れの確認が遅れるだけで、作業は失われない。
- 待ち状態の通知 (Q4) は変わらない。飛ばしたリポジトリは、動作もなく、cuminがOwnerなしで進めるIssueもないリポジトリだからである。
- ログ。作業中でなくなったときと、作業中に戻ったときに、リポジトリごとに1行ずつ出す。飛ばすたびには出さない。
- `idle_poll_interval` が0のとき (テストが `Service` を直に作るとき) は、飛ばさない。設定ファイルからは、`poll_interval` より短い値を指定できない。

### 定期確認で読む内容

対象のリポジトリごとに、開いていて `cumin/type/requirement` の付いたIssueを起点にして、次を読む。Pull Requestとその下の項目は、2つ目の問い合わせで読む (下の「2つの問い合わせ」)。項目の名前は、GraphQLのスキーマで確かめた (実測 55 と、2026-09-20 の introspection)。

| 読むもの | 項目 | 使う行 |
|---|---|---|
| 要求Issueと、そのsub-issue。番号、id、開閉、今のラベル | `Issue.subIssues`、`labels` | R1〜R6、I1 |
| sub-issueの題。依頼のブランチの名前に使う | `Issue.title` | I1 |
| sub-issueのGraphQLのid。I2がリンクを付けるときに使う。スカラーなので、問い合わせのコストは変わらない | `Issue.id` | I2 |
| 状態ラベルが付いた時刻。定期確認の問い合わせとは別の、小さな問い合わせで読む (「ラベルの時刻の読み取り」) | `timelineItems(itemTypes: [LABELED_EVENT])` の `createdAt` と `label` | R3、レビューのラウンド、I13、I15 |
| 最新の `cumin/status/ready` を付けたアカウント。着手の候補 (R1、I1) では判定の前に、ほかの起動ではAgentを起動する前に、別の小さな問い合わせで読む (「Ownerのreadyの確認 (R1、I1)」「Ownerのログイン名の読み取り」) | `timelineItems(itemTypes: [LABELED_EVENT])` の `createdAt`、`label`、`actor { __typename login }` | R1とI1の条件 (Ownerのready)、起動の依頼の事実 (どのroleでも) |
| blocked by のIssueの開閉。要求Issueとsub-issueの両方 | `Issue.blockedBy` | R1、I1 |
| Issueを閉じる、開いているPull Request。番号、作成者、先頭のコミット、ブランチの名前 | `Issue.closedByPullRequestsReferences`、`author { __typename login }`、`headRefOid`、`headRefName` | I1、I2 (リンクがあるか)、I4、I6、I7、I11 |
| 開いているPull Requestの、今のラベル | `PullRequest.labels` | I11 |
| 開いているPull Requestが、既定のブランチにmergeできるか。`MERGEABLE`、`CONFLICTING`、`UNKNOWN` の3つ | `PullRequest.mergeable` | I14 |
| 先頭のコミットの時刻 | `PullRequest.commits(last: 1)` の `commit { oid committedDate }` | I15 |
| レビュー。出した人、結果、対象のコミット、時刻 | `PullRequest.reviews` の `author`、`state`、`commit`、`submittedAt` | I5〜I8、レビューのラウンド |
| 先頭のコミットのcheckの結果 | `PullRequest.statusCheckRollup` の `contexts` | I3、I4 |
| 既定のブランチと、その先頭のコミット | `Repository.defaultBranchRef` の `name` と `target.oid` | リポジトリの設定 |
| リポジトリの設定とriskの基準 | `Repository.object(expression: "HEAD:.cumin/config.toml")` と同 `.cumin/risk-criteria.md` の `Blob` の `oid`、`text`、`byteSize`、`isBinary`、`isTruncated` | リポジトリの設定 |

![定期確認の問い合わせ](poll-snapshot.svg)

図の元ファイル: [poll-snapshot.puml](poll-snapshot.puml)

2つの問い合わせ:

- 1回の定期確認は、GitHubを2つの問い合わせで読む。1つ目 (`snapshotQuery`) はsub-issueまでで止まり、番号、id、題、開閉、閉じた時刻、ラベル、blocked by を読む。2つ目 (`pullRequestsQuery`) は、選んだsub-issueだけについて、Issueを閉じる開いているPull Request (`closedByPullRequestsReferences` と、その下の全ての項目) を読む。上の表のPull Requestの行は、どれも2つ目の問い合わせで読む。
- 分ける理由はポイントである。Pull Requestは、1ページ17ポイントのうち14ポイントを占めていた (2026-10-03に実測、[#421](https://github.com/cloveclovedev/cumin-works/pull/421))。今の値は、下の「ポイント」の項目にある。Pull Requestを読む行が当てはまるsub-issueは、少ない。
- 選ぶのは、開いていて `cumin/status/*` のラベルが付いたsub-issueである。`internal/workflow` の純粋関数 `Snapshot.SubIssuesWithPullRequestRules` が、1つ目の読み取りから選ぶ。選んだsub-issueがなければ、2つ目の問い合わせを送らない。
- `Service.Poll` が、2つの読み取りから1つのスナップショットを作る (`Snapshot.WithPullRequests`)。`Decide` と各行の判定は、そのスナップショットだけを読む純粋関数のままである。選ばなかったsub-issueは、Pull Requestなしでスナップショットに入る。
- `cumin status` はラベルだけを読むので、1つ目の問い合わせだけを送る。

選んだsub-issueが、Pull Requestを読む行の対象を全て含む理由:

| 行 | 対象のsub-issue | 含む理由 |
|---|---|---|
| I1 (readyの実装Issueに着手) | 開いていて `cumin/status/ready` | `cumin/status/ready` は状態ラベルである |
| I3 (checkが通り、reviewへ)、I4 (checkが失敗し、修正へ)、I14 (衝突の解消の依頼)、I15 (checkが結果を返さない) | 開いていて `cumin/status/checking` | `cumin/status/checking` は状態ラベルである |
| I12 (Ownerの承認のあとのmerge)、I13 (Ownerの指摘への対応の依頼)、I14 (衝突の解消の依頼) | 開いていて `cumin/status/awaiting-merge-decision` | `cumin/status/awaiting-merge-decision` は状態ラベルである |
| I11 (Pull Requestにラベルを写す) | 開いていて状態ラベルのあるsub-issue | cuminが作るPull Requestは、状態ラベルのある実装Issueのものである。状態ラベルが変わるたびに、選んだsub-issueとして写す |

- I11は、状態ラベルのない開いたsub-issueと、閉じたsub-issueのPull Requestには、ラベルを写さなくなる。閉じたsub-issueのPull Requestはmerge済みで、開いていない。Ownerが状態ラベルを全て外したsub-issueでは、Pull Requestに前のラベルが残り、Ownerが次に状態ラベルを付けたときに写し直す。写したラベルは判定に使わない (原則5) ので、判定は変わらない。
- 実行終了の判定 (I2、I5〜I8、I10) は、定期確認ではなく、1つのIssueの読み取りでPull Requestを読む (「実行終了のあとの読み取り」)。この読み取りは変わらない。

読む時点が2つになっても判定が正しい理由:

- 判定の入口は、1つ目の時点のsub-issueのラベルである。Pull Requestの事実は、それよりあとの2つ目の時点のものになる。1つの行が読むPull Requestの事実 (先頭のコミット、check、レビュー、`mergeable`) は、どれも2つ目の問い合わせの1回の応答から来るので、互いに食い違わない。
- 動作は、どの行でも読み取りよりあとに起きる。問い合わせが1つのときも、スナップショットは動作の時点より古かった。各動作は、ラベルを先に替えることと、動作の前の確かめ (mergeの手順の読み直しなど) で、これに耐えるように作ってある。2つ目の時点は動作に近いので、Pull Requestの事実はむしろ新しくなる。
- 2つの時点のあいだにsub-issueへ状態ラベルが付いたとき。そのsub-issueは選ばれず、Pull Requestなしで入る。そのsub-issueに当てはまる行は、1つ目の時点のラベルで判定するので、今回は出ない。次の定期確認で読む。ラベルが1つ目の読み取りの直後に付いたときと同じである。
- 2つの時点のあいだにPull Requestがmergeされるか閉じられたとき。sub-issueは開いたまま、Pull Requestなしで入る。I3、I4、I12〜I15は、Pull Requestがなければ何もしない。I1は、Pull Requestがなければ新しいブランチで依頼する。これは、問い合わせが1つのときに、Ownerが手でPull Requestを閉じたあとのスナップショットと同じ形である。
- 2つの時点のあいだにPull Requestが開いたか、pushが入ったとき。新しいほうの事実で判定する。1つ目の時点より古い事実で判定することはない。
- 2つの時点のあいだにsub-issueが消えたか、別のリポジトリへ移ったとき。2つ目の問い合わせがエラーを返し、そのリポジトリの今回の定期確認を止める。欠けたスナップショットでは判定しない。

2つ目の問い合わせの上限とポイント:

- sub-issueは、1つ目の問い合わせで読んだidで指定する (`nodes(ids:)`)。1回に100件までで、101件ではGitHubがエラーを返す (2026-10-03 にcumin-worksで実測)。選んだsub-issueが100件を超えるときは、100件ごとに分けて送る。
- Pull Requestは1つのsub-issueに2件まで、その下のラベル、check、レビューは100件までである。超えたときは、Issueの番号を示すエラーにして、そのリポジトリの定期確認を止める。上限は、1つのIssueの読み取りと同じ値である。
- ポイント (2026-10-03 にcumin-worksで `rateLimit { cost }` を実測)。1つ目の問い合わせは1ページ3ポイントである。2つ目の問い合わせは、sub-issueが1件でも9件でも1ポイント、100件で9ポイントである。

Pull Requestの読み方:

- 読むのは、開いているPull Requestだけである (`includeClosedPrs` を付けない)。定期確認の判定でPull Requestを見る行は、どれも開いているPull Requestを対象にする。閉じたPull RequestはI2に通らず、merge済みのPull RequestはIssueを閉じるので、判定の対象にならない。閉じたPull Requestまで読むと、Ownerの対応待ちや閉じたIssueに溜まった古いPull Requestが1つのIssueで上限 (5件) に達し、そのリポジトリの定期確認が止まりうる。開いているものだけなら、通常は1つのIssueに1件で、上限には届かない。
- merge済みのPull Requestが要るのはフォローアップノート (I9) だけで、下に書いたとおり別に読む。

- GraphQLの `author` は、GitHub Appが作ったPull Requestでは `Bot` 型で、`login` に `[bot]` が付かない (2026-09-22 にsandboxで実測)。RESTの `user.login` と、Agentがコミットに使う身元は `<slug>[bot]` である。GitHubクライアントが `Bot` の `login` に `[bot]` を足して、判定には `<slug>[bot]` の形だけを渡す。
- 作成者のアカウントが消えていると `author` は null になる。判定には空の作成者として渡す。

mergeできるかと、先頭のコミットの時刻の読み方:

- `mergeable` は、GitHubの3つの値 (`MergeableState`) をそのまま、専用の型でスナップショットに入れる。3つ以外の値が来たら、そのリポジトリの定期確認をエラーにする。意味を知らない値の上で判定しないためである。`UNKNOWN` は、GitHubがまだ計算している印であり、エラーではない。
- 先頭のコミットの時刻は、`Commit.committedDate` である。`Commit.pushedDate` は、GitHubがもう返さない (スキーマに「no longer supported」とある)。項目は、公式のGraphQL reference (Objects の `PullRequest` と `Commit`、Enums の `MergeableState`) と、2026-10-03 の introspection で確かめた。
- 先頭のコミットは、`commits(last: 1)` で読む。`PullRequest.headRef` は接続ではないのでコストを変えないが、cumin-worksの開いているPull Requestで `null` を返したので使わない (実測 128)。
- `commits(last: 1)` のコミットが `headRefOid` と違うとき (2つの項目のあいだにpushが入ったとき) は、時刻を空にする。次の定期確認で読み直す。時刻が空のあいだ、I15は決めない。
- `mergeable` は、I14の判定が読む (「checkを待つ間の衝突の解消の依頼 (I14)」)。先頭のコミットの時刻は、I15の判定が読む (「必須のcheckが結果を返さないときの停止 (I15)」)。mergeの手順 (I6、I12) がRESTで読む `mergeable` は、これとは別で、変わらない。

ラベルが付いた時刻の使い方:

- R3は、sub-issueに `cumin/status/ready` が付いた時刻が、要求Issueに `cumin/status/awaiting-plan-review` または `cumin/status/awaiting-acceptance` が付いた時刻よりあとかどうかで判定する。
- レビューのラウンドは、実装Issueに最後に `cumin/status/ready` が付いた時刻と、`cumin-reviewer` の最後の `APPROVE` の時刻の、新しいほうよりあとに出たレビューを数える (「レビューのラウンドの数え方」)。
- I15の待ち時間は、実装Issueに最後に `cumin/status/checking` が付いた時刻と、先頭のコミットの時刻の、遅いほうから数える。cuminは、ラベルの時刻をsub-issueごとにスナップショットに入れる。
- I13は、Ownerのレビューが出された時刻が、実装Issueに最後に `cumin/status/awaiting-merge-decision` が付いた時刻よりあとかどうかを見る。cuminは、この時刻をsub-issueごとにスナップショットに入れる (「Ownerのレビューへの対応の依頼 (I13)」)。
- 同じラベルが何度も付くので、ラベルごとに、いちばん新しい `LabeledEvent` を使う。今付いているかどうかは、`labels` で見る。

閉じた要求Issueと、そのsub-issueは読まない。cuminは、閉じた要求Issueには何もしないためである (Issueのラベルと状態遷移の原則6)。

Pull Requestのラベルは、I11でIssueのラベルと比べるためだけに読む。判定には使わない (原則5)。

ブランチの名前を読むのは、続きの依頼のためである。Issueを閉じる開いているPull Requestがあるときは、その名前をそのまま使い、題から作り直さない。

checkの結果の読み方:

- `statusCheckRollup` の `contexts` は、check run (GitHub Actionsなど) と commit status の2種類を返す。cuminは、どちらからも名前 (`CheckRun.name`、`StatusContext.context`) と結論だけを読む。必須のcheckの一覧が、この名前で書かれているためである。
- 結論は、通った (`SUCCESS`、`SKIPPED`、`NEUTRAL`)、落ちた、まだ終わっていない、の3つに畳む (実測 51)。終わっていない check run (`status` が `COMPLETED` でない) と、`EXPECTED`、`PENDING` の commit status は、まだ終わっていないものとして扱う。
- 知らない種類の context が来たら、そのリポジトリの定期確認をエラーにする。読めない check の上でI3を通すより、止まって知らせるほうがよい。
- 必須のcheckがGitHub Appに紐づいているとき (rulesetの `integration_id`。sandboxの `cumin-protected-paths` がそれである) は、そのAppが出したcheckだけが条件を満たす。GitHubも同じに扱う。cuminは、必須のcheckのAppのidと、check runの `checkSuite.app.databaseId` を持ち、名前とAppの両方で照らす (I3、I4が使う)。commit statusにはAppのidがないので、Appを指定した必須のcheckは満たせない。この項目を足してもコストは変わらない (接続ではないため)。
- 必須のcheckの一覧は、この問い合わせでは読めないのでRESTで読む (`GET /repos/{owner}/{repo}/rules/branches/{branch}`、実測 53)。読むのは、`cumin/status/checking` のIssueがそのリポジトリに1つ以上あるときだけである。RESTの上限はGraphQLと別なので、問い合わせのポイントは増えない。
- ラベル、checkの結果、レビュー、先頭のコミット (`commits`) は、Pull Requestの下の接続なので、1件のPull Requestにつき1ずつコストの係数を上げる。これらの接続は2つ目の問い合わせにあり、選んだsub-issueだけについて読む。Pull Requestを2件までにして、2つ目の問い合わせを、sub-issueが9件までで1ポイント、100件で9ポイントに収めている (2026-10-03に実測、[#449](https://github.com/cloveclovedev/cumin-works/pull/449))。接続の中の件数 (ラベル、check、レビュー、blocked by) はコストを変えないので、100件まで読む。式と見積もりは [cumin本体の設計メモ](cumin-core.md) の「GitHubクライアント」にある。

失敗したcheckの内容の読み方 (I4の依頼に入れる):

- 落ちた必須のcheckごとに、RESTで2つ読む。check runのannotation (`GET /repos/{owner}/{repo}/check-runs/{id}/annotations`) と、そのjobのログの終わり (`GET /repos/{owner}/{repo}/actions/jobs/{job_id}/logs`) である。check runのidとjobのidは、先に読む `GET /repos/{owner}/{repo}/commits/{sha}/check-runs` から取る。jobのidは `details_url` の最後の部分である (実測 54)。
- 落ちたcheckだけを読む。通ったcheckには呼び出しを出さない。annotationは `failure` のものだけを採る。
- ログは終わりだけを採る。jobが失敗した理由は終わりにあるためである。読みながら末尾の2,000バイトだけを残すので、ログが長くてもメモリは増えない。上限の64MiBに達したときは、そこで読むのをやめ、「ここで読むのをやめた。jobの終わりではない」と文章の先頭に書く。1つのcheckの文章は4,000バイトまでにし、切ったことを文章に書く。切る位置は文字の切れ目に合わせる。この3つの数は、要件の設定の表にないので、コードの定数にする。
- 必須のcheckがAppを指定しているときは、そのAppのcheck runの内容だけを読む。判定 (I3、I4) と同じ決まりである。同じ名前のcheckを2つのAppが出していても、別のAppの内容が混ざらない。結果は、落ちた必須のcheckの並びで返す。同じ名前を2つのrulesetが別のAppで求めていても、それぞれの文章が残る。
- annotationは全てのページを読む。失敗のannotationが、警告100件の次のページにあることがあるためである。集めた失敗が文章の上限を超えたら、そこで読むのをやめる。
- 読めなかったときは、エラーにしない。文章はcheckの名前と「内容を読めなかった」だけになり、警告をログに出す。名前だけでも依頼を出す価値があるためである。commit statusにはcheck runがないので、この道に入る。
- 読むのは、落ちたcheck runだけである。同じ名前のcheck runが2つあり、あとの1つが通っている (定期確認のあとにやり直された) ときに、通ったほうの内容を「失敗の内容」として渡さないためである。
- jobのログを読むのは、`details_url` が `/actions/runs/<番号>/job/<番号>` の形のときだけである。GitHub Actions以外のAppの `details_url` は、そのAppのものであり、末尾の数字はjobの番号ではない。
- 公開リポジトリでは、Checks と Actions の権限がなくても読める (実測 54)。privateリポジトリでは読めないことがあるが、v0.1の対象は公開リポジトリである。

フォローアップノート (I9) で使うものは、閉じたsub-issueについてだけ、別に読む (「フォローアップノート (I9)」)。

### レビューのラウンドの数え方

- レビューは、定期確認の問い合わせで、開いているPull Requestごとに100件まで読む。出した人 (Appは `<slug>[bot]` の形)、結果 (`state`)、対象のコミット、出した時刻、アドレスである。100件を超えるPull Requestがあれば、他の接続と同じく、そのリポジトリの定期確認をエラーにする。レビューの一部だけでラウンドを数えないためである。Pull Requestの下の接続が1つ増えるので、1ページのコストは11ポイントから14ポイントになった (2026-09-30にsandboxで実測。[cumin本体の設計メモ](cumin-core.md) の「GitHubクライアント」の式のとおり)。
- 実装Issueに最後に `cumin/status/ready` が付いた時刻は、R3と同じラベルの時刻の問い合わせを、その実装Issueの番号で呼んで読む (「ラベルの時刻の読み取り」)。1ポイントである。読むのは、ラウンドが要る場面 (Reviewerへの依頼と、その実行の終わり) だけである。
- ラウンドに数えるのは、`cumin-reviewer` のレビューのうち、結果が `CHANGES_REQUESTED` のものである。`COMMENTED` だけのレビューはReviewerの結果ではなく (Reviewerの要件の「完了の条件」)、cuminが依頼し直すので、ラウンドに数えない。`PENDING` は、まだ出ていないレビューである。
- `DISMISSED` のレビューは、`APPROVE` と同じく数え直しの起点にする。GitHubは今の結果だけを返し、取り下げる前の結果を返さない。rulesetの "Dismiss stale pull request approvals when new commits are pushed" が取り下げるのは承認なので、`DISMISSED` の多くは元の `APPROVE` である。これをラウンドに数えると、その承認より前のラウンドが数え直されず、上限に早く達する。人が `CHANGES_REQUESTED` を取り下げたときはOwnerの介入と同じなので、数え直してよい。
- 修正を求めたレビューのあとでは、数えた数がそのレビューのラウンドである。I5は上限 (`max_review_rounds`) 未満で修正を依頼し、I8は上限で止める。次にReviewerに依頼するラウンドは、数えた数に1を足したものになる (I3)。
- 2ラウンド目以降の依頼には、最後のラウンドのレビューの対象のコミットを入れる。Reviewerは、そこから今の先頭のコミットまでの差分と、前の指摘を見る。
- Reviewerの実行の終わりに確かめるのは、`cumin-reviewer` が最後に出したレビューである。結果は問わず (`PENDING` を除く)、`COMMENTED` だけのものも最後のレビューになる。
- 最後に承認したコミットは、`cumin-reviewer` のレビューのうち、結果が `APPROVED` で一番新しいものの対象のコミットである (`LastApprovedCommit`)。`cumin/status/ready` の時刻は見ない。Ownerが付け直しても、承認した部分は変わらないためである。`DISMISSED` のレビューは数えない。GitHubは取り下げる前の結果を返さないので、承認だったかどうかが分からないためである。ほかの人の承認と、`PENDING` のレビューも数えない。
- 判定は、どれも `internal/workflow` の純粋関数 (`ReviewRounds`、`LastReviewedCommit`、`LastApprovedCommit`、`LatestReview`) である。GitHub上の事実だけから数えるので、cuminが再起動しても同じ数になる。

### ラベルの時刻の読み取り

- R3は、要求Issueに `cumin/status/awaiting-plan-review` または `cumin/status/awaiting-acceptance` が付いた時刻と、sub-issueに `cumin/status/ready` が付いた時刻を比べる。時刻は、GitHubがIssueのタイムラインに残す `LabeledEvent` の `createdAt` から読む。
- 定期確認の問い合わせには入れず、時刻が要る要求Issueのときだけ、別の問い合わせで読む。要るのは4つの場合である。1つは、要求Issueが `cumin/status/accepting` のときで、そのラベルが最後に付いた時刻を読む (Plannerの質問のコメントが、そのあとに書かれたかを見る)。1つは、R3が成り立ちうるとき、つまり要求Issueが `cumin/status/awaiting-plan-review` または `cumin/status/awaiting-acceptance` で、`cumin/status/ready` の付いた開いているsub-issueがあるときである。もう1つは、`cumin/status/checking` の付いた開いているsub-issueがあるときで、そのsub-issueにラベルが最後に付いた時刻を読む (I15の待ち時間の起点)。最後の1つは、`cumin/status/awaiting-merge-decision` の開いているsub-issueのPull Requestで、今の先頭のコミットに、人の `CHANGES_REQUESTED` のレビューがあるときで、そのsub-issueに `cumin/status/awaiting-merge-decision` が最後に付いた時刻を読む (I13)。判定の純粋関数 (`NeedsLabelTimes`) がこれを決める。
- 1回の問い合わせで、要求Issueと、そのsub-issue (15件まで) のタイムラインを読む。各Issueは、新しいほうから100件の `LabeledEvent` を読み (`last: 100`)、ラベルごとに一番新しい時刻を使う。同じラベルが付いたり外れたりするためである。コストは1ポイントだった (2026-09-29にcumin-worksで実測)。
- 読むのは状態ラベルがその形のあいだだけなので、ふだんの定期確認のコストは変わらない。要求Issueが `cumin/status/accepting` の間と、Ownerが分割結果を確認している間 (前の分割の `cumin/status/ready` が残っているとき) と、sub-issueがcheckを待っている間と、Ownerの `CHANGES_REQUESTED` が今の先頭のコミットに残ったままsub-issueがOwnerの判断を待っている間は、その要求Issueごとに、定期確認のたびに1ポイント増える。1つの要求Issueで2つ以上が要るときも、問い合わせは1回である。
- `cumin/status/checking` の時刻だけが読めなかったときは、ほかの行を止めない。R3が成り立ちえない要求Issueでは、着手 (I1) も待たない。
- 読めなかったときは、I13の候補にしない。Issueは `cumin/status/awaiting-merge-decision` のままなので、次の定期確認でやり直す。
- 読めなかったときは、ログに出して、R3をその定期確認では判定しない。その要求Issueのsub-issueの着手 (I1) も、次の定期確認まで待つ。着手すると `cumin/status/ready` が外れ、R3が二度と成り立たなくなるためである。R3がラベルを替えられなかったときも、同じ理由で待つ。ほかの行は進める。
- 採らなかった案: 定期確認の問い合わせに、sub-issueごとのタイムラインを入れる。1ページに要求Issue 10件 x sub-issue 15件のタイムラインが加わり、ページを小さくしても、R3が要らない定期確認のたびにコストが増える。

### Ownerのログイン名の読み取り

- 起動の依頼の事実「Ownerのログイン名」([Agentに共通の要件](../requirements/agents/common.md) の「起動の依頼の事実」) のために、Agentを起動する前に、その実行が扱うIssueに最新の `cumin/status/ready` を付けたアカウントを読む。GitHubがIssueのタイムラインに残す `LabeledEvent` の `actor` から読む。
- 問い合わせは、ラベルの時刻の問い合わせと同じ形に `actor { __typename login }` を足したものである (`ReadLabelActor`)。1回で、そのIssueと、そのsub-issue (15件まで) のタイムラインを、新しいほうから100件ずつ読む。Issue自身にイベントがあれば、その中で一番新しいものを使う。なければ、sub-issueのイベントの中で一番新しいものを使う (イベントのない要求Issue)。実装Issueにはsub-issueがないので、同じ問い合わせで足りる。
- そのアカウントがOwnerかどうかは、I12と同じ読み取り (`RepositoryPermission`) と同じ判定 (`IsOwner`) で決める。Ownerの定義は [cumin本体の要件](../requirements/cumin-core.md) の「Owner」だけにある。次のどれかのときは、Ownerのログイン名はない: イベントがない、`actor` がnull (アカウントがもうない)、`actor` が人ではない (`__typename` が `User` でない。GitHub Appは `Bot`)、権限がwrite未満である。人ではないときは、権限を読まない。
- コストは、GraphQLが1ポイント (2026-10-03にcumin-worksで実測。`LabeledEvent` に `actor` があることも、スキーマで確かめた) と、人のときのRESTの呼び出し1回である。起動のたびに増えるだけで、ふだんの定期確認のコストは変わらない。R1とI1の着手では、判定の前の読み取りを使うので、起動のときには増えない。
- R1とI1では、判定の前の読み取り (「Ownerのreadyの確認 (R1、I1)」) がスナップショットに入れた名前を使い、読み直さない。ほかの起動では、次のとおりに読む。
- 読むのは、ラベルを替える前である。順は、ログイン名を読む、ラベルを替える、依頼する、になる。読めなければ、ラベルを替えず、依頼もしない。次の定期確認でやり直す (I3のラウンドの読み取りと同じ形)。ラベルを依頼より先に替えることは変わらない (原則3)。
- 読む場所は、定期確認が起動を決める所である: R4 (受け入れの確認)、I3 (review)、I4 (checkの修正)、I14 (checkまたはOwnerの判断を待つ間の衝突の解消)、I13 (Ownerのレビューへの対応)、I12 (Ownerの承認のあとのmerge) の衝突の解消。I12では、mergeが衝突したときだけ、ラベルを替える前に読む。読めなければ、Issueは `cumin/status/awaiting-merge-decision` のままなので、次の定期確認でI12がもう一度成り立つ。
- 1つの実行の続きで出す依頼は、その実行の前に読んだ名前を使い、読み直さない: 異常終了のあとのやり直し、I5の指摘の修正、I8の原因の整理、I6 (Reviewerの承認のあとのmerge) の衝突の解消。I6で読み直さないのは、そこで一時的でない失敗で読めないと、Issueが `cumin/status/reviewing` のまま残り、どの定期確認もやり直さないためである。
- 読むのは、各Issueの新しいほうから100件のラベルのイベントだけである。最新の `cumin/status/ready` のあとに100件を超えるラベルのイベントがあると、そのイベントはないものとして扱う (要求Issueはsub-issueから読み、実装Issueは「ない」になる)。sub-issueから読むのは、依頼に書くログイン名だけである。R1とI1の条件の確認 (「Ownerのreadyの確認 (R1、I1)」) は、sub-issueから読まず、「Ownerでない」にする。
- 読んだ名前は、`internal/agent` が事実のかたまりに書く ([Agentの実行の設計](agent-run.md) の「Claude Codeの起動」)。
- 採らなかった案: ラベルの時刻の問い合わせに `actor` を足して、1つの問い合わせにまとめる。R3とラウンドの読み取りは `actor` を使わず、2つの読み取りは使う場面も違うので、分けたままにした。

### Ownerのreadyの確認 (R1、I1)

- R1とI1は、そのIssueに最新の `cumin/status/ready` を付けたのがOwnerであるときだけ成り立つ ([Issueのラベルと状態遷移](../requirements/workflow/issue-states.md) の「Ownerのready」)。そのため、判定の前に、着手の候補ごとに、付けたアカウントとその権限を読み、結果をスナップショットに入れる (`readReadyOwners`)。
- 読むIssueは、純粋関数 `ReadyActorReads` が決める。R1とI1の候補 (開いていて、`cumin/status/ready` が付き、blocked by が全て閉じている。I1では `cumin/type/owner-task` がない) を、着手の順 (優先度、Issueの番号) に並べて返す。同時に進めるIssueの数に空きがなければ、1つも返さない。着手の候補がない定期確認と、空きがない定期確認では、アカウントも権限も読まない。
- 読み取りは、「Ownerのログイン名の読み取り」と同じ問い合わせである。ただし、そのIssue自身のイベントだけを使い、sub-issueのイベントには頼らない (`ReadOwnLabelActor`、`RepositoryPermission`、`IsOwner`)。候補には `cumin/status/ready` が付いているので、読んだ100件のイベントの中にreadyのイベントがなければ、「Ownerでない」として扱う。sub-issueから読むと、triageのアカウントが要求Issueにreadyを付け、ほかのラベルを100回付け外ししてイベントを読む範囲の外に出すだけで、sub-issueのOwnerのreadyで分割を始められてしまうためである。Ownerの定義は、I12と同じ `IsOwner` だけにある。人ではないアカウントの権限は読まない。
- 着手の順に読み、Ownerのreadyが空きの数だけ見つかったら、そこで止める。それよりあとの候補は、この定期確認では着手できないためである。
- スナップショットには、Issueごとに「読んだ」(`ReadyRead`) と、Ownerのログイン名 (`ReadyOwner`。Ownerでなければ空) を入れる。判定 (`readyRequirementIssues`、`readySubIssues`) は、Ownerのreadyと読めた候補だけを残す。Ownerでない候補と、読んでいない候補は飛ばす。飛ばした候補は空きを使わないので、同じ定期確認で、ほかのOwnerのreadyに着手する。
- Ownerでないアカウントのreadyには、ラベルを替えず、依頼もしない。ログに1行 (warn) 残し、Ownerに1回通知する。同じreadyのイベントについては、定期確認のたびに繰り返さない。伝えたイベントの時刻を、Issueごとにメモリに持つ (`readyTold`)。通知は多くても1回である: 送れなかったときも、ログにエラーを残すだけで、送り直さない。同じIssueに、Ownerでないアカウントが新しくreadyを付けたときは、別のイベントなので、もう一度伝える。cuminが再起動すると、もう一度だけ伝える (失っても作業を失わない手元の状態)。
- 読めなかったときは、ログにエラーを出し、そのIssueは「読んでいない」のままにする。その定期確認では着手せず、次の定期確認で読み直す。ほかの行は進める。
- 待ち状態の通知 (Q4) では、Ownerでないreadyと読めたIssueを「Ownerなしで進めるIssue」に数えない。読んでいないready (空きがない、読めなかった) は、これまでどおり数える (`MovesWithoutOwner`)。
- 実行を待ってから止める間は、新しい着手をしないので、読まない。
- コストは、読む候補1つにつき、GraphQLが1ポイントと、人のときのRESTの呼び出し1回である (「Ownerのログイン名の読み取り」の実測)。着手するIssueでは、これまで起動の前に読んでいた分が判定の前に移るだけで、増えない。増えるのは、候補が着手できないまま残る間である: Ownerでないready、利用枠で止まっている着手 (Q1)、読み取りの失敗。その間は、定期確認のたびに、読む候補1つにつき同じコストがかかる。Ownerのreadyが空きの数だけ見つかれば止めるので、1回の定期確認で読む数は、空きの数と、その前に並ぶOwnerでないreadyの数の和までである。
- 確かめた公式のページ: GraphQLの [LabeledEvent](https://docs.github.com/en/graphql/reference/objects#labeledevent) (`actor`、`createdAt`、`label`)。2026-10-04に、スキーマの問い合わせ (`__type(name: "LabeledEvent")`) でも、`actor` が `Actor` (nullになりうる) であることを確かめた。権限は、RESTの [Get repository permissions for a user](https://docs.github.com/en/rest/collaborators/collaborators#get-repository-permissions-for-a-user) である (I12と同じ)。
- 採らなかった案: 着手を適用するとき (ラベルを替える直前) に確かめる。判定が、Ownerでないreadyに空きを割り当ててしまい、同じ定期確認でほかのIssueに着手できない。判定の前に読めば、判定は純粋関数のままで、空きを正しく分けられる。
- 採らなかった案: 読んだ結果を、次の定期確認まで持ち越す。readyを付け直したことは、イベントを読まないと分からないので、候補であるあいだは毎回読む。

### 要求Issueのコメントの読み取り

- R4とR7は、Plannerの受け入れの確認のコメントが、最後のsub-issueが閉じたあとに書かれたかを見る。問い合わせは `issueOrPullRequest` で、IssueにもPull Requestにも答える。I8も、Pull Requestのコメントを同じ問い合わせで読む。`issue(number:)` はPull Requestの番号を解決しない (2026-09-30にcumin-worksで確かめた。NOT_FOUNDになる)。sub-issueが閉じた時刻は、定期確認の問い合わせで `closedAt` として読む。スカラーの項目なので、コストは変わらない。
- コメントは、R4かR7が成り立ちうる要求Issueのときだけ、別の問い合わせで読む。成り立ちうるのは、要求Issueが `cumin/status/accepting` のときと、`cumin/status/implementing` で、sub-issueが1つ以上あり、全て閉じているときである (`NeedsComments`)。新しいほうから50件ずつ、最後のsub-issueが閉じた時刻に届くまで遡って読む (`comments(last: 50, before: ...)`)。ふつうは1ページで届き、コストは1ポイントだった (2026-09-30にcumin-worksで実測)。決まった件数だけを読むと、受け入れの確認のあとにコメントが多く付いたとき、確認のコメントが読む範囲から外れる。Plannerは同じ回の自分のコメントを書き直すだけで、書き直しても並び順と作成の時刻は変わらないので、R4が依頼を繰り返してしまう。
- 数えるのは、作成者がPlannerのAppのbot (`<slug>[bot]`) で、1行目が `## Acceptance check` のコメントだけである。表の結果は読まない。同じ読み取りから、Plannerの質問のコメント (作成者が同じbotで、1行目が `## Decision needed` で始まる) の時刻も取る。botのloginは、AppのJWTで `GET /app` を1回読んで作り、覚えておく。
- 読めなかったときは、ログに出して、その要求IssueではR4もR7も判定しない。次の定期確認で読み直す。
- R4は、閉じたsub-issueのフォローアップノート (I9) を書き終えるまで待つ。I9は判定の前に動き、要求Issueごとに「もう書くノートがない」ことを `FollowUpsDone` に残す。書くノートがないとは、閉じたsub-issueのそれぞれについて、ノートがあるか、mergeされずに閉じたか、拾うものがないことである。この定期確認で読めなかったり書けなかったりしたら、R4は待ち、次の定期確認でやり直す。最後のsub-issueが閉じた回は、同じ定期確認の中で、ノートを書いてから受け入れの確認を依頼する。R7は待たない。受け入れの確認のコメントは、R4が依頼したあとにしか書かれないので、ノートより先にならない。

### 閉じたIssueの片付け

- 定期確認の問い合わせのあと、判定の前に、別の手順として行う。GitHubには何も書かない。
- 対象を決めるのは純粋関数 `IssuesToCleanUp` で、スナップショットの閉じたsub-issueのうち、Agentが動いていないものを返す。何を消し、何を残すかは [Agentの実行の設計](agent-run.md) の「作業場所」にある。
- 定期確認が失敗したリポジトリでは、片付けない。スナップショットがないためである。

### フォローアップノート (I9)

- 定期確認の問い合わせのあと、判定の前に、別の手順として行う。I9は要求Issueのラベルを替えず、Agentも起動しないので、判定の着手リストには入れない。
- 対象は、スナップショットにある開いている要求Issueの、閉じたsub-issueである。閉じた要求Issueはスナップショットにないので、何も読まず、何も書かない (原則6)。
- 要求Issueごとに、まずコメントを読む (「要求Issueのコメントの読み取り」と同じ問い合わせ)。読むのは、閉じたsub-issueのうち一番早く閉じた時刻よりあとのコメントである。ノートは、sub-issueが閉じたあとにしか書かれないためである。
- ノートの最後の行には、目に見えない目印 `<!-- cumin:follow-up-note issue=<sub-issue> pull-request=<Pull Request> notes=<番号,...> -->` を置く。`notes` は、そのsub-issueでノートが要るPull Requestの全てである。1つのsub-issueに、ノートの要るmerge済みのPull Requestが2つ以上あるとき、cuminは全てを読んでから書く。途中で書き込みに失敗しても、`notes` のうちノートのないものが残るので、次の定期確認がそのsub-issueを読み直す。`notes` のない目印は、自分のPull Requestだけを表す。結び付いたPull Requestのうち、まだ開いているものも `notes` に入れる。sub-issueが閉じたあとにmergeされたとき、そのノートを書くためである。開いたまま閉じられたPull Requestは、ノートが付かないので、要求Issueが開いている間、そのsub-issueを読み直し続ける (1回に1ポイント)。R4は、まだ開いているPull Requestを待たない。目印を数えるのは、cumin-coreのAppのbotが書いたコメントだけである。公開リポジトリでは誰でもコメントを書けるので、ほかの人の目印でノートが止まらないようにする。botのloginは、cumin-coreのAppのJWTで `GET /app` を1回読んで作り、覚えておく。
- 閉じた時刻以降に書かれた目印があり、その `notes` の全てにノートがあるsub-issueは、もう読まない。ないsub-issueだけ、閉じるよう結び付いたPull Requestの一覧を読む (`closedByPullRequestsReferences` に `includeClosedPrs` を付ける。閉じたものとmerge済みのものも返る)。誰がsub-issueを閉じたかは見ない。2026-09-30から、GitHubはmergeでIssueを閉じないことがあり、そのときはcumin (#222) かOwnerが閉じるためである (#239 のOwner役のNote)。一覧のうち、merge済みで、まだ目印のないPull Requestごとに、説明とレビューのスレッドを1回の問い合わせで読む (公式: GraphQLのスキーマの `PullRequest.reviewThreads`。2026-09-30にintrospectionで確かめた)。どちらの問い合わせも、コストは1ポイントだった (2026-09-30にcumin-worksとsandboxで実測)。結び付いたPull Requestが10件を超えたら、読み取りの誤りにする。
- merge済みのPull Requestが結び付いていなければ、ノートは書かない。mergeせずに閉じたPull Requestだけのときと、結び付いたPull Requestがないときである。結び付いたmerge済みのPull Requestが2つ以上あれば、それぞれに1つずつ書く。GitHubが結び付けなかったときの扱いは #272 が決める。
- 拾うものは2つである。1つは、説明の `## Follow-up` の見出しから、次の見出しか次の罫線 (`---` だけの行) までの文章で、テンプレートの `<!-- -->` を除いてそのままコピーする。`Follow-up` はテンプレートの最後の節なので、罫線がないと、CLIが説明の最後に足す署名までコピーしてしまう。テンプレートは、節のあとに罫線を置く。コードブロックの中の罫線では止めない。空か `None` なら、ないものとする。もう1つは、スレッドの最初のコメントが、ReviewerのAppのbotの `<ラベル> (non-blocking):` で始まるスレッドである。そのうち、ラベルが `praise` と `note` でないもので、返答のどれも `Fixed` か `Answer` で始まらないものを拾う。返答した人は問わない。
- スレッドの行は、今の行 (`line`) を使う。コードが動いて今の行がないときは、書かれたときの行 (`originalLine`) を使う。どちらもなければ、ファイルの名前だけを書く。
- レビューのスレッドか、1つのスレッドのコメントが100件を超えたら、読み取りの誤りとして記録し、ノートを書かない。一部だけを全体として写さないためである。
- 拾うものがなければ、ノートを書かず、目印も残さない。そのPull Requestは、要求Issueが開いている間、定期確認のたびに読み直す。コストは、目印のない閉じたsub-issue1つにつき、一覧の1ポイントと、merge済みのPull Request1つにつき1ポイント、それに要求Issueのコメントの1ポイントである。手元に「読んだ」記録を持たず、GitHubの事実だけで決めるためである。
- 読めなかったとき、書けなかったときは、ログに出して、次の定期確認でやり直す。その要求IssueのR4は、それまで待つ (「要求Issueのコメントの読み取り」)。
- 判定の純粋関数は `FollowUpCandidates`、`FollowUpNote` などで、`internal/workflow/followup.go` にある。
- 採らなかった案: 拾うものがないPull Requestも、手元のメモリに覚えて読み直さない。cuminが再起動するまでの読み直しは減るが、手元の記録で判定することになる。コストが問題になったら、改めて考える。

### リポジトリの設定の読み取り

対象のリポジトリの `.cumin/config.toml` と `.cumin/risk-criteria.md` は、定期確認の問い合わせで一緒に読む。何を上書きできるかと、優先順位は要件にあり、キーの一覧は [設定の一覧](../development/configuration.md) にある。ここでは、どう読むかだけを決める。

- 読む場所は `HEAD:` である。GraphQL の `expression` の `HEAD` はそのリポジトリの既定のブランチを指すので、Pull Requestのブランチの内容は入らない。要件のとおり、`.cumin/` の変更はOwnerがmergeしたものだけが効く。
- 読む頻度は、定期確認のたびである。要求Issueのページが複数になるときは、1ページめだけで読む (変数 `$repositoryFiles` と `@include`)。1回の定期確認が読むのは1組でよいためである。
- 追加のコストはない。`object` と `defaultBranchRef` は接続 (connection) ではないので、問い合わせのポイントは変わらない。2026-09-22に実測し、足す前と足したあとのどちらも `cost` は6だった。
- `text` を解析するのは `internal/workflow` の側である。同じ内容を毎回解析しないように、`Blob` の `oid` を一緒に返す。oid はgitのblobのハッシュなので、内容が変われば変わる。
- ファイルがなければ、`object` は `null` を返す。これはエラーではなく、Hostの設定がそのまま効く。
- `isBinary` が真、`text` が `null`、`isTruncated` が真のいずれかなら、そのリポジトリの読み取りをパスの名前を添えたエラーにする。ルールが、ファイルの一部だけを見て動くことを防ぐ。1MiB を超えるファイルは `isTruncated` になる。
- 採らなかった案: REST の `GET /repos/{owner}/{repo}/contents/{path}?ref=<既定のブランチ>`。どちらも `Contents` の read で呼べる (公式: Permissions required for GitHub Apps) が、RESTだとリポジトリごとに1回の定期確認で2回の要求が増え、Issueの事実とファイルの時点がずれる。

読んだあとの扱い:

- 定期確認は、リポジトリごとに「Hostの設定に `.cumin/config.toml` を重ねた設定」と「riskの基準の文章と、その出どころ」と「保護されたパスの一覧」を持つ。保護されたパスの一覧は、`protected_paths` の値であり、ファイルかキーがなければ初期値である。持ち回すのは `internal/workflow` で、GitHubの型は入らない。
- 解析するのは、`Blob` の `oid` が変わったときだけである。変わらない間は、前の結果を使う。
- ファイルが誤っていたら、そのリポジトリの定期確認をやめる。エラーにはファイルの名前とキーの名前が入る。他のリポジトリの定期確認は続く。誤りの結果は持たないので、次の定期確認で読み直し、Ownerがmergeで直せば自動で戻る。同じ理由で続けて失敗したときは、次の話題のとおりOwnerに知らせる。
- Agentの起動には、そのリポジトリのroleの設定 (`cli`、`model`) と、保護されたパスの一覧を渡す。一覧は、どのroleの起動にも、実行の事実として渡す。cuminは一覧の中身を確かめない。確かめるのは、GitHub Actionsのcheck (`cumin-protected-paths`) だけである。Agentの接続部分は、依頼に設定が付いていればそれを使い、なければHostの設定を使う。
- ログに出すのは、設定の出どころ (`host` か `repository`) と、riskの基準の出どころ (`default`、`host`、`repository`) だけである。riskの基準の文章はログに出さない。

### 定期確認の判定

![定期確認の判定](poll-decide.svg)

図の元ファイル: [poll-decide.puml](poll-decide.puml)

- 判定は `internal/workflow` の純粋関数である。スナップショットと、設定 (リポジトリごとに同時に進めるIssueの数、優先度のラベルの一覧、checkの待ち時間)、定期確認の時刻だけから、着手リストを返す。時刻は値として受け取り、時計を読まない。I/Oをしない。同じスナップショットからは、Issueの並び順によらず、同じ着手リストを返す。着手可能なIssue数は、定期確認のたびにラベルから数え直す。ファイルにもメモリにも持ち越さない。
- 進行中として数えるのは、`cumin/status/planning` と `cumin/status/accepting` の要求Issueと、`cumin/status/implementing`、`cumin/status/checking`、`cumin/status/reviewing` の開いているsub-issueである。`cumin/status/implementing` の要求Issue (R3) は、Agentが動いていないので数えない。数えると、初期値の上限 (1) では、どのsub-issueにも着手できなくなる。
- 動作の適用は、判定とは別の部分が行う。着手では、ラベルを替えてから依頼する (Issueのラベルと状態遷移の原則3)。ラベルを替えられなければ依頼せず、次の定期確認でやり直す。
- 今の判定はR1、R3、R4、R6、R7、`cumin/status/accepting` の出口 (「request the acceptance check again」と「stop the acceptance check for the Owner」)、I1、I3、I4、I11、I12、I13、I14、I15である。I9は判定の前の別の手順である (「フォローアップノート (I9)」)。R2、I2、I5〜I8、I10は、実行の終わりに判定する。
- R3、R6、R7は、要求Issueのラベルを替えるだけで、Agentを起動しない。そのため一番先に決め、上限の空きを使わない。R4は、R1、I1と同じく上限の空きを分け合い、同じ順番 (優先度、Issueの番号) で着手する。
- R4は、要求Issueのラベルを `cumin/status/accepting` に替えてから依頼する。Ownerの `cumin/status/ready` は要らない。`cumin/status/accepting` の要求Issueは、Plannerが動いていなくても、同時に進めるIssueの数に数える。
- `cumin/status/accepting` の出口は、純粋関数 `AcceptanceEnd` が、要求Issueの事実と「Plannerが動いているか」(`Snapshot.Running`) から決める。定期確認も、Plannerの実行の終わりも、同じ関数で決める。そのため、確認の途中でcuminが再起動しても、次の定期確認が同じ結果を出す。
  - Plannerが動いている間は、何もしない。
  - 最後のsub-issueが閉じたあとに書かれた受け入れの確認のコメントがあれば、R7 (「ask the Owner to accept」) である。
  - Plannerの質問のコメントが、`cumin/status/accepting` が付いた時刻以降に書かれていれば、「stop the acceptance check for the Owner」である。cuminはコメントを書かず、ラベルを `cumin/status/awaiting-decision` に替えて通知する。
  - どちらのコメントもなければ、「request the acceptance check again」である。この `cumin/status/accepting` の間に1回だけ依頼し直す。依頼し直した回数は、Hostの状態ファイルに持つ。依頼し直したあとにもコメントがなければ、ラベルを `cumin/status/awaiting-decision` に替えてから、理由をコメントに書き、通知する。ラベルを替えられなければ、コメントも通知も出さず、状態ファイルの回数も消さない。次の定期確認が、依頼せずに同じ判定をやり直す。
  - コメントかラベルの時刻を読めなかったときは、決めない。次の定期確認が決める。
- 状態ファイルを失うと、回数は0に戻る。そのときは、もう1回だけ余分に依頼する。Plannerは、同じ回の自分のコメントを書き直すので、コメントは増えない (Plannerの要件の「やり直しに備えること」)。
- 要求Issueが `cumin/status/implementing` のままで、受け入れの確認のコメントが既にあるとき (ラベルを移す前のcuminが依頼した確認) は、今までどおり、依頼せずにR7で `cumin/status/awaiting-acceptance` に替える。
- R3は、要求Issueに状態ラベルがないときは `cumin/status/ready` の付いた開いているsub-issueがあれば成り立ち、`cumin/status/awaiting-plan-review` または `cumin/status/awaiting-acceptance` のときは、そのラベルよりあとに `cumin/status/ready` が付いたsub-issueがあれば成り立つ。R6は、`cumin/status/implementing` の要求Issueに開いているsub-issueがあり、その全てに状態ラベルがないときに成り立つ。`cumin/type/owner-task` のsub-issueも、状態ラベルがないので数える。
- R6の通知は、ラベルを替えたあとに1回だけ出す。次の定期確認では要求Issueがもう `cumin/status/implementing` ではないので、同じ通知を繰り返さない。ラベルを替えられなければ通知せず、次の定期確認でやり直す。
- I14、I3、I4、I15は、着手 (R1、I1) より先に決める。`cumin/status/checking` のIssueは、同時に進めるIssueの数に既に数えられているので、先に決めても着手の枠を奪わない。
- checkの判定は純粋関数である。必須のcheckの一覧と、先頭のコミットのcheckの結果から、通った・落ちた・待ちの3つのどれかを返す。決まりは次のとおりである。
  - 必須のcheckが1つもなければ、すぐ通ったとみなす。
  - 名前が同じ結果が、その必須のcheckに当たる。rulesetがAppを指定していれば、そのAppの結果だけが当たる。
  - 1つでも落ちていれば、ほかがまだ終わっていなくても「落ちた」にする。I4の修正は、待つより先である。
  - 結果がない、または終わっていない必須のcheckがあれば「待ち」にする。必須のcheckの一覧はpushの前から決まっているので、現れていないcheckは、これから現れるcheckである。
  - 必須でないcheckは、落ちていても判定に入らない。
- I4は、`cumin/status/checking` のIssueで、Pull Requestの先頭のコミットの必須のcheckが「落ちた」ときに成り立つ。動作には、落ちた必須のcheckを、Appも含めて持たせる。上限に達したかどうかは、Hostの状態ファイルの回数を読む適用の側で、純粋関数 (回数 < `max_check_fix_requests`) に聞く。回数はGitHubの事実ではないので、スナップショットには入れない。
- I14は、`cumin/status/checking` または `cumin/status/awaiting-merge-decision` のIssueで、スナップショットのPull Requestの `mergeable` が `CONFLICTING` のときに成り立つ。`UNKNOWN` と `MERGEABLE` では成り立たない。`UNKNOWN` は、GitHubがまだ計算している印なので、あとの定期確認が決める。I14が成り立つIssueには、I3もI4も出さない。1つの定期確認では、最初に成り立った行だけを動かすためである。
- `cumin/status/awaiting-merge-decision` のIssueのI14は、I12とI13の候補のあとに並べる。実行中のIssue (I12のmergeの手順が動いているIssue) と、`cumin/status/ready` も付いているIssue (I1が扱う) は除く。適用の順は「checkを待つ間の衝突の解消の依頼 (I14)」に書く。
- I15は、`cumin/status/checking` のIssueで、checkの判定が「待ち」のまま、checkの待ち時間 (リポジトリの設定 `checks_wait_time`) を過ぎたときに成り立つ。I14、I3、I4のあとに決めるので、衝突したPull RequestはI14、落ちたcheckはI4になり、I15にはならない。`mergeable` が `UNKNOWN` のままでも成り立つ。待ち時間の起点は、ラベルの時刻と先頭のコミットの時刻の、遅いほうである。待っている間に新しいコミットがpushされると、起点が新しくなる。ラベルの時刻か、先頭のコミットの時刻を読めなかった定期確認では決めず、あとの定期確認が決める。先頭のコミットの時刻が空のときは、新しいコミットがpushされた直後かもしれないためである。動作には、先頭のコミット、結果を返していない必須のcheck (結果がない、または終わっていない)、待った時間を持たせる。実装Issueを閉じる開いているPull Requestがないとき (誰かが閉じたなど) も、ラベルの時刻から待ち時間を過ぎたら成り立つ。先頭のコミットがないので、起点はラベルの時刻だけである。動作には、Pull Requestの番号を0にして、待った時間だけを持たせる。
- I3は、`cumin/status/reviewing` に替えたあと、Reviewerを起動する (「Reviewerへの依頼 (I3、I10)」)。
- I11は、Issueを閉じる開いているPull Requestごとに、そのラベルのうち `cumin/status/*` と `risk/*` を、Issueのものに置き換える。ほかのラベルは残す。並び順によらず同じなら、書き込まない。書き込みは、Issueと同じ "Set labels for an issue" で行う。GitHubでは、Pull RequestもこのAPIのIssueである。
- I11がコピーするのは、その定期確認で読んだIssueのラベルである。同じ定期確認や実行の終わりで替えたラベルは、次の定期確認でPull Requestに届く。ラベルは、OwnerがPull Requestの一覧で見るためのもので、判定には使わないので、この遅れは困らない。
- I1は、`cumin/type/owner-task` の付いたsub-issueに着手しない。`cumin/status/ready` が付いていても同じである。同時に進めるIssueの数は、ほかのIssueと同じく状態ラベルで数える。Ownerの作業のIssueはふつう状態ラベルを持たないので、数に入らない。作業の途中で `cumin/type/owner-task` が付いたIssueは、そのラベルのあいだAgentが動きうるので、数に入れたままにする。
- R1とI1は、どちらもAgentを起動するので、同じ上限の空きを分け合う。候補を合わせて、優先度の高い順、同じ優先度ならIssueの番号の昇順に並べ、先頭から空きの数だけ着手する。どちらかの行を先にする決まりは置かない。優先度が同じなら、Ownerが先に書いたものが先に進み、表形式のテストで結果が1つに決まる。
- 優先度は、純粋関数 `PriorityRank` が、Issueのラベルと設定の一覧から順位 (0が最も高い) にする。R1とR4は要求Issueのラベルで、I1はsub-issueのラベルで決め、sub-issueに優先度のラベルがなければ要求Issueのラベルで決める。どちらにもなければ、一覧の長さを順位にするので、どのラベルよりもあとになる。ラベルの名前は、GitHubと同じく大文字と小文字を区別せずに比べる。
- R1とI1の候補は、最新の `cumin/status/ready` を付けたのがOwnerであると、判定の前に読めたものだけである (「Ownerのreadyの確認 (R1、I1)」)。Ownerでない候補と、読んでいない候補は、着手リストに入れず、空きも使わない。
- 並べ替えるのは、着手できる候補だけである。blocked by のIssueが開いている候補は先に落ちていて、空きの数は並べ替えのあとで当てるので、優先度はどちらも越えない。利用枠は、着手を適用するときに確かめる (次の項目)。
- 優先度のラベルの一覧は、リポジトリの設定を重ねたあとの値を渡す。`priority_labels` はリポジトリの設定ファイルにだけ書ける。Hostの設定に書けるようにすると、cuminは初期値のラベルを作らず、`scripts/setup-repo.sh` もHostの設定を読まないので、ラベルを用意する人がいなくなる。書かれていないリポジトリでは、初期値のラベルを使い、足りないものをcuminが作る。作るのは、起動時ではなく定期確認の中である。設定に書かれているかどうかは、リポジトリの `.cumin/config.toml` を読むまで分からないためである。設定を読み直すたびに1回だけ確かめ、失敗したら次の定期確認でやり直す。設定に書かれたラベルはOrganizationのものなので、cuminは作らず、変えない。
- R1とI1は、ラベルを替える前に、使用率を読んで上限と比べる (Q1)。上限に達していれば、ラベルも手元の状態も変えずに、その着手を飛ばす。次の定期確認で、判定からやり直す。手順は [利用枠の設計](quota.md) の「着手の前の確認」にある。
- Ownerが実行を待ってから止めるよう頼んだあと ([cumin本体の設計メモ](cumin-core.md) の「実行を待ってから止める」) は、判定の結果から、Agentに新しい依頼を出す動作を落としてから適用する。判定そのものは変えない。
- 採らなかった案: 定期確認の中で、GitHubを読みながら判定する。判定の途中で事実が変わりうるうえ、表形式のテストができない。

### Implementerへの依頼

- ブランチの名前は `cumin/<Issue番号>-<短い説明>` で、cuminが決めて渡す (Implementerの要件の「入力」)。短い説明は、実装Issueの題から作る。小文字にし、`a`〜`z` と `0`〜`9` 以外の文字の連続を1つの `-` にし、先頭と末尾の `-` を除き、40文字以内に収まる単語の並びを先頭から残す (先頭の単語だけで40文字を超えるときは、その単語を40文字で切る)。何も残らなければ `issue` にする。例: 題が "Add the login screen" のIssue #10 は `cumin/10-add-the-login-screen` になる。
- 名前を題から作るのは、最初の依頼のときである。そのIssueを閉じる開いているPull Requestが既にあれば、そのPull Requestのブランチを使う。2つ以上あれば、I2の検証と同じく、番号が最も大きいものを使う。題が変わっても、既にあるPull Requestのブランチは変わらない。
- Pull Requestが既にあるときの着手 (I1) は、依頼の種類が「続き」になる (Implementerの要件の「いつ起動されるか」)。worktreeは、そのPull Requestのブランチ (`origin/<ブランチ>`) から作る。前のラウンドのworktreeが残っていれば、消してから作り直す。残ったworktreeは、別のブランチの上にあるか、そのあとにpushされたコミットより遅れていることがあり、新しいセッションはGitHubの事実から始めるためである。ただし、GitHubにない作業 (コミットしていない変更、pushしていないコミット) を持つworktreeは消さずに、そのまま使う。cuminが止めた実行の作業がそこに残るためである ([Agentの実行の設計](agent-run.md) の作業場所)。依頼文には、Pull Requestの番号と、新しいPull Requestを作らずに同じPull Requestにコミットを積むことを書く。I1なので、セッションは新しい。
- 依頼文は `internal/workflow` の純粋関数が組み立てる。「実装」の依頼文に入れるのは、依頼の種類、リポジトリ、実装Issueの番号、ブランチ、作業場所と、1つのPull Requestを開く短い指示 (説明を書く前にskill `cumin-pull-request` を呼ぶこと、`Closes #<番号>` を書くこと) である。依頼の種類によらないことは、roleの指示にあり、依頼文には書かない。
- 起動の依頼には、依頼文とは別に、扱うIssueの事実を入れる。Implementerでは、どの種類の依頼 (実装、続き、checkの修正、指摘の修正、衝突の解消) でも、実装Issueの番号と、種類「implementation issue」と、Ownerのログイン名 (「Ownerのログイン名の読み取り」) である。`internal/agent` が、依頼文の先頭の事実のかたまりに書く ([Agentの実行の設計](agent-run.md) の「Claude Codeの起動」)。
- 採らなかった案: 短い説明をAgentに決めさせる。名前がGitHubの事実になる前にcuminが知っている必要があり、続きの依頼でも同じ名前を渡すためである。

### Plannerへの依頼 (R1、R4)

- 要求Issueのラベルを `cumin/status/planning` に替えてから、Plannerに依頼する。替えられなければ依頼せず、次の定期確認でやり直す。I1と同じ形である。
- 作業場所は、要求Issueの番号とPlannerの組のworktreeで、既定のブランチの先頭をdetachedで開く ([Agentの実行の設計](agent-run.md) の「作業場所」)。ブランチは作らない。依頼のたびに、前の依頼のworktreeを消してから作り直す。受け入れの確認は、mergeされた全てのsub-issueを含むmainを読む必要があり、分割のときのworktreeは古いためである。Plannerは何も書かないので、消して失うものはない。異常終了のあとのやり直しは、同じworktreeで続ける。
- R4では、要求Issueのラベルを `cumin/status/accepting` に替えてから依頼する。替えられなければ依頼せず、次の定期確認でやり直す。「受け入れの確認」の依頼文には、依頼の種類、リポジトリ、要求Issueの番号、作業場所と、mergeされた作業の上でRequirementsを1項目ずつ確かめてコメントする短い指示を入れる。
- 依頼は、新しいセッションで始める。分割 (R1) も受け入れの確認 (R4) も、新しいセッションで始まるためである (Plannerの要件の「いつ起動されるか」)。例外は、受け入れの確認の依頼し直しである。受け入れの確認の実行のセッションを状態ファイルに持ち、依頼し直しは、そのセッションがあれば続きから始める。要求Issueが `cumin/status/accepting` を出るときと、次にR4で入るときに、状態ファイルのその要求Issueの分を消す。
- 「分割」の依頼文に入れるのは、依頼の種類、リポジトリ、要求Issueの番号、作業場所と、分割して計画をコメントする短い指示である。skillの名前、GitHubに残すもの、やり直しへの備えは、roleの指示にある。
- riskの基準は、I1と同じく、リポジトリの3段を解決した本文を起動の依頼で渡す。
- 扱うIssueの事実は、分割 (R1) でも受け入れの確認 (R4) でも、要求Issueの番号と、種類「requirement issue」と、Ownerのログイン名である。I1と同じく、起動の依頼で渡す。Ownerのログイン名は、R1では判定の前の読み取り (「Ownerのreadyの確認 (R1、I1)」) の名前を使い、R4では依頼の前に読む。R4で読めなければ依頼せず、次の定期確認でやり直す (「Ownerのログイン名の読み取り」)。
- 分割の実行の終わりは、R2のきっかけになる。判定は「実行終了の判定」にある。受け入れの確認の実行の終わりは、`done` でも異常終了でも、要求Issueと、そのラベルの時刻とコメントを読み直して、定期確認と同じ `AcceptanceEnd` で決める。コメントがあればR7、なければ同じ実行の中で1回だけ依頼し直し、それでもなければOwnerに戻す。異常終了のたびに同じ依頼をやり直すことはしない。読み直しが失敗したら何もせず、次の定期確認が決める。`blocked` だけは、GitHubから読まずに、行の番号をR4にして、R2と同じ手順でOwnerに戻す (`blocked_reason` をcuminがコメントに書く)。cuminが止まる途中なら、依頼し直さず、ラベルも替えない。

### Agentの実行の並行化

![着手とAgentの実行の並行化](poll-start.svg)

図の元ファイル: [poll-start.puml](poll-start.puml)

- 着手 (I1) は、ラベルを替えたあと、worktreeの用意からAgentの実行の終わりまでを、定期確認とは別のgoroutineで進める。定期確認とAgentの実行は並行して走り、定期確認は実行を待たない。1回の実行は最長50分続くので、待つと、その間に他のリポジトリの定期確認も、他のIssueの着手も止まる。
- goroutineはIssueごとに1つである。同時に走る数は、着手の判定が数える進行中のIssueの数 (「定期確認の判定」) で決まる。実行中のIssueの集合は、ラベルから分かるので手元に持たない。
- 依頼の種類は、I1の「実装」と「続き」、I4の「checkの修正」、I5の「指摘の修正」、I13の「Ownerのレビューへの対応」、I6、I12、I14の「衝突の解消」である。どれも同じ手順 (worktreeの用意、起動、実行の終わりの判定) を通り、違うのは行の番号、ブランチ、再開するセッション、依頼文だけである。Reviewerの依頼は、同じ形の別の手順である (「Reviewerへの依頼 (I3、I10)」)。
- worktreeの用意に失敗したときは、Issueの番号を添えてログに出して、そのgoroutineを終える。ラベルは `cumin/status/implementing` のまま残る。cuminを止めたときに取り消された `git clone` の失敗も、Agentの異常終了ではなく、この用意の失敗として扱う (実測 96)。辻褄の合わないIssueの回収は、v0.1では作らない ([Issueのラベルと状態遷移](../requirements/workflow/issue-states.md) の「v0.1では実装しないこと」)。
- 実行の終わりが、実行終了のきっかけになる (原則1)。判定は次の話題にある。
- cuminがすぐに止まるときは、動いている実行を待ってから終わる。実行のcontextは定期確認のcontextなので、止めると実行は異常終了 (種類は「実行時間の上限」) になる。実行を待ってから止めるときは、contextを終わらせないので、実行は自分で終わる。
- 採らなかった案: 実行の終わりを、別の仕組み (キュー、ファイル) に記録して、次の定期確認で拾う。実行はcuminの子プロセスなので、終わりはその場で分かる (原則1)。記録を挟むと、失っても困らないはずの手元の状態が増える。

### checkの修正の依頼 (I4)

- I4を適用する順は、回数を1つ増やして状態ファイルに書く、ラベルを `cumin/status/implementing` に替える、失敗したcheckの内容を読む、依頼する、である。回数を先に書くのは、書けない状態ファイルで上限を越えて依頼し続けないためである。書けなければラベルを替えず、次の定期確認でやり直す。ラベルを依頼より先に替えるのは、同じ修正を二重に依頼しないためである (原則3)。ラベルを替えられなければ、依頼せずに回数を元に戻す。替えられない失敗が続いても、1回も修正しないまま上限に達しないためである。
- 上限 (リポジトリの設定 `max_check_fix_requests`) に達していれば、依頼しない。「Ownerに戻す道」の手順を、行の番号I4で呼ぶ。ただし、ラベルを先に `cumin/status/awaiting-decision` に替え、替えられたときだけコメントと通知に進む。定期確認が決める停止なので、ラベルが替わらないと、次の定期確認が同じ停止を決め、コメントと通知を繰り返すためである。実行の終わりが決める停止 (I2) は、実行ごとに1回しか起きないので、コメントを先に書く順のままにする。コメントと通知には、落ちたcheckの名前と、依頼した回数を書く。
- 依頼は、状態ファイルにある直前の実行のセッションを `--resume` で再開する。セッションがなければ (状態ファイルを失ったとき)、新しいセッションで始まる。依頼文には、落ちたcheckごとに、その内容 (「失敗したcheckの内容の読み方」) を載せる。内容はcheckの出力なので、指示ではなくデータとして読むよう依頼文に書く。
- worktreeは、そのIssueのものを使う。ブランチは、Pull Requestのブランチである。前のworktreeは、続きの依頼 (I1) と同じく、GitHubにない作業を持っていなければ消して、`origin/<ブランチ>` から作り直す。ブランチの名前が変わったときや、新しいPull Requestができたときも、Pull Requestのブランチの上で直せる。
- 実行の終わりは、I1の依頼と同じに扱う。`done` ならI2の検証をもう一度行い、通れば `cumin/status/checking` に戻る。`blocked` ならOwnerに戻す。異常終了は1回だけやり直す。やり直しは新しいセッションで行い、異常終了したセッションを再開しない。
- 回数とセッションは、Ownerが `cumin/status/ready` を付け直したとき (I1) に消える。次の依頼は新しいセッションで、回数0から始まる。

### 必須のcheckが結果を返さないときの停止 (I15)

- I15は、Agentに依頼しない。「Ownerに戻す道」の手順を、行の番号I15で呼ぶ。上限に達したI4と同じく、ラベルを先に `cumin/status/awaiting-decision` に替え、替えられたときだけコメントと通知に進む。定期確認が決める停止なので、ラベルが替わらないと、次の定期確認が同じ停止を決め、コメントと通知を繰り返すためである。ラベルが替わると、次の定期確認ではI15が成り立たないので、停止は1回だけである。
- コメントと通知には、同じ1文を入れる。Pull Requestの先頭のコミット、結果を返していない必須のcheckの名前、待った時間を書く。開いているPull Requestがないときは、そのことと待った時間を書き、コメントの `Pull request:` は `None` になる。理由は書かない。cuminは見える事実だけを書き、理由はOwnerが調べる。
- 定期確認の時刻は、利用枠の判定と同じ時計 (`Service.now`) から1回読み、判定に値として渡す。
- 実行を待って止める間も、この停止は適用する。Agentを起動しないためである。

### checkを待つ間の衝突の解消の依頼 (I14)

- GitHubは、衝突のあるPull Requestで `pull_request` のワークフローを動かさない。そのため、衝突したままでは必須のcheckが結果を返さず、I3もI4も成り立たない。I14は、定期確認のスナップショットの `mergeable` から衝突を見つけて、Implementerに戻す。
- I14を適用する順は、Ownerのログイン名を読む、ラベルを `cumin/status/implementing` に替える、依頼する、である。ラベルを依頼より先に替えるのは、同じ依頼を二重に出さないためである (原則3)。ログイン名を読めないとき、ラベルを替えられないときは、依頼せず、Issueは `cumin/status/checking` のまま残る。次の定期確認でやり直す。
- 依頼は、mergeが衝突したとき (I6、I12) と同じ「衝突の解消」である。依頼文も同じで、既定のブランチの名前を入れる。依頼文の最初の文は、Pull Requestが既定のブランチと衝突していることだけを言い、mergeが失敗したとは言わない。I14では、cuminはまだmergeを呼んでいないためである。
- 依頼は、状態ファイルにあるImplementerのセッションを `--resume` で再開し、別のgoroutineで動かす。worktree、ブランチ、実行の終わりの扱いは、checkの修正 (I4) と同じである。`done` ならI2の検証を行い、通れば `cumin/status/checking` に戻る。
- checkの修正を依頼した回数には数えない。衝突はImplementerの誤りではなく、並行して進むほかのPull Requestのmergeで起きるためである ([Issueのラベルと状態遷移](../requirements/workflow/issue-states.md) の「checkを待つ間の行」)。
- 衝突の解消の実行が `done` で終わっても、Pull Requestの先頭のコミットが衝突したときのままなら、I2に進まずに、行の番号I14でOwnerに戻す。そのまま `cumin/status/checking` に戻すと、次の定期確認が同じ依頼を出し続けるためである。
- 実行を待って止める間は、この依頼を落とす (`WithoutNewWork`)。Issueの状態ラベルは変わらないので、次の起動の定期確認でI14がそのまま成り立つ。
- I14は、Ownerの判断を待つ間 (`cumin/status/awaiting-merge-decision`) にも成り立つ。ほかのPull Requestのmergeで衝突したPull Requestを、Ownerが承認する前にImplementerに戻す。Ownerは、mergeできる先頭のコミットだけを判断すればよい。適用の手順、依頼文、先頭のコミットが変わらないときの停止 (行の番号I14) は、checkを待つ間と同じである。ログイン名を読めないとき、ラベルを替えられないときは、Issueは `cumin/status/awaiting-merge-decision` のまま残り、次の定期確認でやり直す。解消のあとは、I2、必須のcheck、Reviewerのレビュー (I3) を通り、I7でもう一度Ownerの判断を待つ。
- Ownerの判断を待つ間のI14は、同じ定期確認のI12とI13のあとに適用する。衝突した先頭のコミットにOwnerのレビューがあるときは、そのレビューが先に決める。I12が動いたとき (mergeの手順、または停止) と、I13が差し戻したときは、そのIssueのI14を適用しない。Ownerの承認は、今までどおりI12を通り、mergeの衝突から「衝突の解消」になる。Ownerが承認していても、必須のcheckが通っていなくてI12がmergeを待つときは、I12は動いていないので、I14を適用する。衝突したPull Requestでは、checkがもう動かないためである。I12かI13の確認がエラーで終わったときも、適用しない。次の定期確認が決める。Ownerのレビューかどうかは、権限を読まないと分からないので、純粋な判定は候補を並べるだけにして、適用の側が落とす。Ownerでない人のレビューしかないときは、I12もI13も動かないので、I14を適用する。
- `UNKNOWN` と `MERGEABLE` では、Ownerの判断を待つIssueは何も変わらない。I15は `cumin/status/checking` だけの行なので、`UNKNOWN` が続いても止めない。
- `mergeable` は、定期確認の2つ目の問い合わせが、選んだsub-issueのPull Requestで読む。`cumin/status/awaiting-merge-decision` は状態ラベルなので、Ownerの判断を待つ開いているIssueは選ばれ、そのPull Requestの `mergeable` も読む (「定期確認で読む内容」の2つの問い合わせの表)。

### Reviewerへの依頼 (I3、I10)

![Reviewerへの依頼](poll-review.svg)

図の元ファイル: [poll-review.puml](poll-review.puml)

- I3を適用する順は、ラウンドを読む、ラベルを `cumin/status/reviewing` に替える、依頼する、である。ラウンドは、ReviewerのAppのbotのloginと、実装Issueに最後に `cumin/status/ready` が付いた時刻と、スナップショットのレビューから数える (「レビューのラウンドの数え方」)。読めなければラベルを替えず、次の定期確認でやり直す。ラベルを依頼より先に替えるのは、同じレビューを二重に依頼しないためである (原則3)。
- ReviewerのAppのbotのloginは、Plannerと同じく、AppのJWTで `GET /app` を1回読んで作り、覚えておく。
- 作業場所は、実装IssueとReviewerの組のworktreeで、Pull Requestの先頭のコミットをdetachedで開く ([Agentの実行の設計](agent-run.md) の「作業場所」)。依頼のたびに、前のラウンドのworktreeを消してから作り直す。ラウンドごとに先頭のコミットが変わるためである。異常終了のあとのやり直しは、同じworktreeで続ける。
- 1ラウンド目は新しいセッションで始める。2ラウンド目以降は、Hostの状態ファイルにあるReviewerのセッションを `--resume` で再開する。Reviewerのセッションは、Implementerのセッションと別の項目 (`reviewer_session_id`) に持つ。2つのroleはセッションを共有しない (Reviewerの要件の「いつ起動されるか」)。セッションがなければ (状態ファイルを失ったとき)、新しいセッションで始まる。Ownerが `cumin/status/ready` を付け直すと、I1が両方のセッションを消す。
- 依頼文 (「review」) に入れるのは、リポジトリ、実装Issue、Pull Request、先頭のコミット、ラウンドと上限、作業場所と、2ラウンド目以降では前のラウンドでレビューしたコミットである。ラウンドごとに見る範囲は、roleの指示にある (Reviewerの要件の「ラウンドごとに見る範囲」)。
- ReviewerがこのPull Requestの前のコミットを承認しているときは、依頼文のラウンドの行の次に、最後に承認したコミットを `Approved commit: <コミット>` の行で入れる。承認のあとはラウンドが1から数え直され、前のラウンドでレビューしたコミットがないので、Reviewerがどこまで承認したかを依頼から読めるようにするためである。1ラウンド目では、指示の文も、そのコミットから今の先頭のコミットまでの差分だけを、1ラウンド目の深さで見るように言う。2ラウンド目以降は、行が加わるだけで、指示の文は変わらない。承認したコミットが今の先頭のコミットのときは、差分がないので、行を入れない。一度も承認していなければ、依頼文は今までと同じである。
- 扱うIssueの事実は、review、やり直しのreview、原因の整理 (I8) のどれでも、実装Issueの番号と、種類「implementation issue」と、Ownerのログイン名である。I1と同じく、起動の依頼で渡す。Ownerのログイン名は、ラウンドと同じく、ラベルを替える前に読む (「Ownerのログイン名の読み取り」)。
- 実行が `done` で終わったら、その実装Issueだけを読み直し (「実行終了の判定」)、ReviewerのAppのbotが最後に出したレビューを確かめる (純粋関数 `CheckReview`)。それが今の先頭のコミットに対する `APPROVE` か `REQUEST_CHANGES` なら、レビューが出たとみなす。そうでなければ、同じセッションで1回だけ依頼し直す。依頼文には、何が見つからなかったかだけを書く。2回目も見つからなければ、行の番号I5 (I5の「うまくいかないとき」) で「Ownerに戻す道」の手順を呼ぶ。
- 読み直したPull Requestの先頭のコミットが、依頼したときと違えば (Reviewerの実行中にOwnerがpushしたときなど)、レビューを確かめずに、ラベルを `cumin/status/checking` に戻す。必須のcheckが通ったのは古いコミットだけだからである。新しいコミットでcheckが走り、I3かI4がもう一度決める。古いコミットに出たレビューは、GitHubにあるとおりにラウンドに数える。
- `APPROVE` なら、「mergeの手順 (I6、I7)」に進む。`REQUEST_CHANGES` なら、「指摘の修正の依頼 (I5)」に進む。
- `blocked` なら、I10である。やり直さず、行の番号I10で「Ownerに戻す道」の手順を呼ぶ。コメントは、Reviewerが書いた `blocked_reason` である。
- Reviewerの実行のあとの手順は、GitHubの呼び出しが一時的な失敗 (`github.IsTemporary`) で終わったときに、捨てずに持っておく。仕組みは、I2と同じである (「実行終了の判定」、`keptstep.go`)。図の2つの戻る矢印がこれである。
  - `done` のあとの手順は、Issueの読み直しから、レビューが決める道のラベルの付け替えまでである。対象の呼び出しは、tokenの発行、Issueの読み直し、承認のあとの読み直しと必須のcheckの一覧 (I6、I7)、ラベルの付け替え (先頭のコミットが動いたとき、I5、I7、checkが通っていないとき) である。
  - `blocked` のあとの手順 (I10) は、Issueの読み直しである。読み直しが一時的な失敗で終わったら、コメントも通知も出さずに持っておく。あとの定期確認で読めたら、コメント、ラベル、通知を1回ずつ出す。
  - 手順は、Issueの読み直しからやり直す。読み直したIssueに `cumin/status/reviewing` がもうなければ、何も書かずに終える。ただし、直前の回に一時的な失敗で終わったラベルの付け替えのラベルが付いていれば、その書き込みは届いていたので、ラベルは書かずに、その先 (I5の依頼、I7の通知) を1回だけ行う。`cumin/status/checking` への付け替えには先がないので、そのまま終える。覚えておくラベルは、直前の回のものだけである。
  - 届いていた付け替えで、持っておく間にIssueが `cumin/status/checking` になることがある。定期確認の判定は、このラベルの行 (I3、I4、I14、I15) でも、実行中のIssue (`Snapshot.Running`) を候補にしない。持っておいた手順が終わる前に、同じIssueのAgentをもう1つ起動しないためである。I2の持っておいた手順でも同じである。
  - ラベルのあとに続く長い処理 (I5の依頼、I8の原因の整理、I6のmerge、レビューが見つからないときの依頼し直し) は、手順の残りである。失敗なく終わった回のあとで、1回だけ始める。持っておく対象ではないので、同じ依頼を二重に出すことはない。定期確認の中でやり直した回では、残りを別のgoroutineで動かし、定期確認は待たない。その間、Issueは作業中のIssueの集合に残る。
  - 一時的でない失敗は、今までどおりに扱う。ラベルを替えずにログに出す。
  - I8の原因の整理の実行のあとの読み取りは、まだ持っておかない。mergeの手順は持っておく (「mergeの手順 (I6、I7)」)。
- 異常終了は、I1の依頼と同じく、同じ作業場所の新しいセッションで1回だけやり直す。2回目も異常終了なら、行の番号I3でOwnerに戻す。
- 採らなかった案: レビューが見つからないときに、すぐOwnerに戻す。Reviewerの要件の「完了の条件」は、1回だけ依頼し直すと決めている。
- 採らなかった案: ReviewerとImplementerのセッションを1つの項目に持つ。I5はImplementerのセッションを、2ラウンド目のレビューはReviewerのセッションを再開するので、1つでは足りない。

### 指摘の修正の依頼 (I5)

- Reviewerの実行の終わりに、先頭のコミットに `REQUEST_CHANGES` が出ていたら、読み直したレビューからラウンドを数え直す。そのレビューのラウンドである。上限 (リポジトリの設定 `max_review_rounds`) 未満ならI5、上限に達していればI8である。判定は純粋関数 (ラウンド < `max_review_rounds`) である。
- I5を適用する順は、ラベルを `cumin/status/implementing` に替える、依頼する、である。ラベルを替えられなければ依頼しない。実行の終わりが決める動作なので、次の定期確認が同じ依頼を決めることはない (Reviewerの実行ごとに1回しか起きない)。ラベルの付け替えが一時的な失敗で終わったときは、手順を持っておき、あとの定期確認で読み直しからやり直す (「Reviewerへの依頼 (I3、I10)」)。
- 依頼は、状態ファイルにあるImplementerのセッションを `--resume` で再開する。Reviewerのセッションではない。worktree、ブランチ、実行の終わりの扱いは、checkの修正 (I4) と同じである。`done` ならI2の検証をもう一度行い、通れば `cumin/status/checking` に戻る。そのあとI3が、次のラウンドのレビューを依頼する。
- 依頼文 (「指摘の修正」) に入れるのは、リポジトリ、実装Issue、Pull Request、ブランチ、作業場所と、レビューのアドレスである。指摘そのものは依頼文に写さない。Implementerが、GitHubで指摘を読み、スレッドごとに返答するためである (返答のテンプレートはskill `cumin-review-reply`)。
- Implementerの依頼は、Reviewerの実行と同じgoroutineで続けて行う。持っておいた手順が定期確認の中で依頼を決めたときは、別のgoroutineで行う。どちらでも、同じIssueのAgentは、いつも1つだけである。
- 上限に達していれば、Implementerには依頼せず、「上限での原因の整理 (I8)」に進む。

### Ownerのレビューへの対応の依頼 (I13)

- 定期確認の判定 (純粋関数) が、候補を集める。集め方はI12の候補と同じで、見るレビューだけが違う。`cumin/status/awaiting-merge-decision` の開いた実装Issueで、Pull Requestの今の先頭のコミットに、人 (botでないアカウント) の `CHANGES_REQUESTED` のレビューがあるものである。実行中のIssueは除く。`cumin/status/ready` も付いているIssueは除く。Ownerが新しい着手を求めているので、I1が扱う。
- 候補にするのは、そのレビューが、実装Issueに最後に `cumin/status/awaiting-merge-decision` が付いた時刻よりあとに出されたときだけである。2つの時刻は、どちらもGitHubの事実である。レビューの時刻は定期確認の問い合わせの `submittedAt`、ラベルの時刻はラベルの時刻の問い合わせで読み、スナップショットのsub-issueに入れる (「ラベルの時刻の読み取り」)。cuminは手元に何も残さない。ラベルの時刻を読めなかった定期確認では、候補にしない。読めても、そのラベルの時刻がないとき (新しいほうから100件の `LabeledEvent` に入っていないとき) も、候補にしない。時刻が分からないまま差し戻すと、同じレビューで繰り返すためである。
- 候補ごとに、判断のレビューを出した人たちの権限を、I12と同じ読み取りと同じ判定 (`IsOwner`) で確かめる。Ownerのレビューのうち、最新の判断のレビューが今の先頭のコミットへの `CHANGES_REQUESTED` で、実装Issueに最後に `cumin/status/awaiting-merge-decision` が付いた時刻よりあとに出されていれば、I13が成り立つ (純粋関数 `OwnerRequestedChanges`)。古いコミットへのレビュー、botのレビュー、Ownerでない人のレビューは数えない。`COMMENTED` は判断のレビューではないので、コメントだけのレビューでは何も起きない。あとから出したOwnerの `APPROVED` は、差し戻しを取り消す。
- I13を適用する順は、Ownerのログイン名を読む、ラベルを `cumin/status/implementing` に替える、依頼する、である。権限かログイン名を読めないとき、またはラベルを替えられないときは、依頼しない。Issueは `cumin/status/awaiting-merge-decision` のままなので、次の定期確認でやり直す。ラベルを替えたあとは候補にならないので、同じ依頼を二度出さない。
- 依頼は、状態ファイルにあるImplementerのセッションを `--resume` で再開し、別のgoroutineで動かす。worktree、ブランチ、実行の終わりの扱いは、checkの修正 (I4) と同じである。`done` ならI2の検証を行い、必須のcheck、Reviewerのレビュー (I3) を通って、I7でもう一度Ownerの判断を待つ。直したコミットで先頭が変わるので、前の `CHANGES_REQUESTED` は古いコミットへのレビューになり、もう数えない。Reviewerのラウンドは、Reviewerの最後の `APPROVE` のあとから数え直すので、1ラウンド目から始まる (「レビューのラウンドの数え方」)。
- 1つの `CHANGES_REQUESTED` で差し戻すのは1回だけである。Implementerがコミットせずに答えると、先頭のコミットは変わらず、Ownerの `CHANGES_REQUESTED` はそのコミットに残る。Issueは、I2、必須のcheck、Reviewerのレビューを通って、I7で `cumin/status/awaiting-merge-decision` に戻る。そのレビューは、このラベルが付いた時刻より前のものなので、I13はもう成り立たず、Ownerの判断を待つ。Ownerがもう一度 `CHANGES_REQUESTED` を出すと、そのレビューはラベルよりあとなので、1回だけ差し戻す。
- 必須のcheckは読まない。I13はmergeしないためである。利用枠 (Q1) と、同時に進めるIssueの数も見ない。新しい着手ではなく、Ownerが求めた続きの作業だからである。実行を待って止める間は、この依頼を落とす (`WithoutNewWork`)。
- 候補を確かめただけの定期確認は、待ち状態の通知 (Q4) では動作に数えない。差し戻したときに数える。I12と同じである。
- 依頼文 (「Ownerのレビューへの対応」、`Request: owner review fix`) は、`internal/workflow` の純粋関数 `OwnerReviewFixRequestText` が組み立てる。入れるのは、リポジトリ、実装Issue、Pull Request、ブランチ、作業場所と、Ownerのレビューのアドレスである。
- 依頼文は、そのレビューとコメントをGitHubで読むこと、Pull Requestのブランチで直すこと、新しいPull Requestを作らないことを伝える。コメントそのものは依頼文に写さない。Implementerが、GitHubでコメントを読み、スレッドごとに返答するためである (返答のテンプレートはskill `cumin-review-reply`)。
- 「指摘の修正」(I5) と別の種類にするのは、Ownerのコメントに `(blocking)` の印がないためである。roleの指示は、指摘の修正では blocking のコメントにだけ返答すると決めている。この依頼では、Ownerのレビューの全てのコメントに対応し、それぞれに返答する。これは依頼文と `roles/implementer.md` の両方に書く。`roles/implementer.md` の指摘の修正の決まりは、種類 `review fix` を名指しするので、この依頼には当たらない。
- レビューの本文にも対応する。本文にはコメントのスレッドがないので、Implementerは、Pull Requestへのコメント1つで本文に答える。これも依頼文と `roles/implementer.md` の両方に書く。
- セッションは、Implementerの直前のセッションの続きである (Implementerの要件の「いつ起動されるか」)。

### mergeの手順 (I6、I7)

![承認されたPull Requestとmergeの手順](poll-merge.svg)

図の元ファイル: [poll-merge.puml](poll-merge.puml)

- Reviewerが今の先頭のコミットを承認したら、その実装Issueと必須のcheckの一覧を読み直し、純粋関数 `DecideMerge` で決める。読むのは、その実装Issueだけである (「実行終了の判定」)。既定のブランチの名前も、同じ問い合わせで読む。必須のcheckは、そのブランチのruleから読むためである。riskは実装Issueのラベルから読む (原則5)。riskのラベルがちょうど1つでなければ、行の番号I6でOwnerに戻す。riskを先に確かめるのは、ラベルの誤りが、checkの状態によらず必ず止まるようにするためである。
- checkの結果は、この読み直しで読んだPull Requestのものを使う。レビューを確かめたあとに、checkがもう一度動くことがあるためである。Pull Requestが見つからないか、先頭のコミットが承認したものと違えば、checkが通っていないものとして扱う。
- 必須のcheckが先頭のコミットで通っていなければ、ラベルを `cumin/status/checking` に戻す。Reviewerの実行中に先頭のコミットが動いたとき (「Reviewerへの依頼」) と同じ扱いで、I3かI4が次の定期確認で決め直す。
- `risk/low` ならmergeの手順に進む (I6)。それ以外の `risk/*` は、ラベルを `cumin/status/awaiting-merge-decision` に替えて、Pull Requestのアドレスを入れた通知を1回出す (I7)。ラベルを替えられなくても、通知は出す。その手順はやり直さないので、Ownerが知る機会はそこだけだからである。ただし、ラベルの付け替えが一時的な失敗で終わったときは、通知を出さずに手順を持っておき、やり直した回で通知を出す。読み直しと必須のcheckの一覧の読み取りが一時的な失敗で終わったときも、手順を持っておく (「Reviewerへの依頼 (I3、I10)」)。
- I7では、ラベルを替えたあと、通知の前に、`cumin-core` がOwnerのレビューを依頼する (`POST /repos/{owner}/{repo}/pulls/{n}/requested_reviewers`、`reviewers` にOwnerのログイン名を1つ。公式: Request reviewers for a pull request。要る権限は Pull requests の書き込み。公式: Permissions required for GitHub Apps。実測 138)。GitHubの「レビューの依頼」の一覧に、Ownerの判断を待つPull Requestだけを載せるためである。Ownerの決定は #305 にある。
  - 依頼する相手は、実装Issueに最新の `cumin/status/ready` を付けたアカウントである。Ownerに当たるアカウントが複数あっても、依頼するのはこの1つだけである。Reviewerの実行の前に読んだ名前を使い、読み直さない (「Ownerのログイン名の読み取り」)。Ownerのログイン名がなければ、依頼しない。
  - 依頼は、通知を出すときに必ず出す。I7が成り立つたびに出るので、Ownerの差し戻し (I13) や衝突の解消 (I14) のあとにも、もう一度出る。すでに依頼してあるアカウントへの同じ依頼は、失敗せず、一覧にも1つのまま残る (実測 139)。
  - 依頼が失敗したら、ログに書くだけにする。Ownerに戻さず、一時的な失敗でも手順を持っておかず、通知はそのまま出す。Ownerは通知で知るので、依頼がなくてもIssueは進むためである。受け入れた不利益は、失敗した回のPull Requestが、次にI7が成り立つまで一覧に載らないことである。協力者でないアカウントへの依頼は422になる (実測 140) が、Ownerはwrite以上の権限を持つので、ふつうは起きない。
  - 依頼は、cuminのどの判断も変えない。レビューの依頼はレビューではなく、I12は今までどおり、Ownerの `APPROVED` のレビューだけを読む (`OwnerApproved`)。
  - 採らなかった案: `CODEOWNERS` のファイルで依頼する。GitHubは、Pull Requestが開いたときに依頼する (公式: About code owners) ので、cuminが自分でmergeする `risk/low` でも、Reviewerの承認の前でも依頼が出て、一覧が「Ownerの判断を待つ」を表さなくなる。ファイルに決まったログイン名を書くことにもなる。
  - 採らなかった案: write以上の権限を持つ人の全員に依頼する。I7のたびに協力者の一覧を読むことになり、全員に全ての依頼が届く。
- mergeの手順は、I6とI12の両方が使う。行の番号は引数で受け取る。
  - `cumin-core` が `PUT /repos/{owner}/{repo}/pulls/{n}/merge` を呼ぶ。`merge_method` はリポジトリの設定、`sha` は承認された先頭のコミットである。承認のあとにpushされたコミットは、mergeしない (409。実測は #286 の M2)。mergeの状態が `clean` になるのは待たない。"Restrict updates" のruleがあるブランチでは、常に `blocked` だからである (実測 62)。
  - 405は、衝突とrulesetの拒否の両方で返る (実測 62、#286 の M4)。405のあとにPull Requestを読み直し、`mergeable` が `false` なら衝突とみなす。merge の前に読んだ `mergeable` は古いことがある (#286 の M4、M5) ので、mergeの前には読まない。
  - 衝突なら、ラベルを `cumin/status/implementing` に替えてから、Implementerに「衝突の解消」を依頼する (I6の失敗の欄)。セッションは、状態ファイルにあるImplementerのセッションの続きである。worktree、ブランチ、実行の終わりの扱いは、指摘の修正 (I5) と同じで、`done` のあとはI2、必須のcheck、I3を通る。依頼文には、既定のブランチの名前を入れる。
  - 衝突の解消は、既定のブランチをPull Requestのブランチにmergeして行う。Implementerの指示は強制pushを禁じており、rebaseしたブランチはpushできないためである。新しい先頭のコミットには、Reviewerの新しい承認が要る。ラウンドは、最後の `APPROVE` から数え直す (「レビューのラウンドの数え方」)。
  - 衝突の解消の実行が `done` で終わっても、Pull Requestの先頭のコミットが衝突したときのままなら、I2に進まずに、行の番号I6でOwnerに戻す。そのままI2に通すと、同じ衝突がレビューとmergeを何度も回るためである。
  - 先頭のコミットが動いたとき (409) と、それ以外の一時的でない失敗 (GitHubの答えを入れる) は、1文でOwnerに戻す。
  - mergeの手順は、GitHubの呼び出しが一時的な失敗 (`github.IsTemporary`) で終わったときに、捨てずに持っておく。仕組みは、I2と同じである (「実行終了の判定」、`keptstep.go`)。図の2つの戻る矢印がこれである。I6でもI12でも同じである。持っておく間、Issueはラベルを保ち、コメントも通知も出さない。
    - 持っておく手順は2つある。「merge」は、トークン、Pull Requestの読み取り、mergeの呼び出しまでである。「mergeのあとに閉じること」は、Issueの読み取りと、閉じる操作である。mergeが済んだあとの失敗で、mergeをやり直さないためである。
    - mergeは取り消せず、書き込みなのでクライアントはやり直さない。答えが届かなくても、GitHubがmergeを済ませていることがある。そこで、持っておいた「merge」をやり直す回は、最初にPull Requestを読む (`GET /repos/{owner}/{repo}/pulls/{n}` の `merged`。公式: Get a pull request)。mergeが済んでいれば、mergeを呼ばずに、閉じる手順に進む。この読み取りが一時的な失敗で終わったら、mergeを呼ばずに、また持っておく。最初の回は読まない。判定で、開いているPull Requestを読んだばかりだからである。
    - mergeの判断は、最初の回の前に決めたものである。持っておく間に、riskのラベル、必須のcheck、Ownerのレビューが変わることがある。そこで、やり直す回は、mergeが済んでいないと分かったあとに、Issueを読み直して同じ純粋関数で決め直す。mergeを呼ぶのは、決め直した結果もmergeのときだけである。I6では、最初の判断と同じ手順 (`decideApproved`) を通るので、`risk/low` でなくなっていればI7、checkが通っていなければ `cumin/status/checking`、riskのラベルの誤りならOwnerに戻す。Issueが `cumin/status/reviewing` でなくなっていれば、何も変えない。I12では、Issueがまだ `cumin/status/awaiting-merge-decision` で、先頭のコミットが同じで、Ownerの最新の判断が `APPROVED` で (`OwnerApproved`)、`DecideMerge` がmergeを許すことを確かめる (`ownerStillApproves`)。成り立たなければ、mergeせずに手順を終える。Issueは実行中でなくなり、次の定期確認が、このラベルの行 (I12、I13、I14) で決める。決め直しの読み取りが一時的な失敗で終わったら、mergeを呼ばずに、また持っておく。
    - やり直す回でも、`sha` は承認された先頭のコミットである。持っておく間にpushされたコミットは、mergeしない (409でOwnerに戻す)。
    - 衝突の解消の依頼と、mergeのあとの待ちは、手順の残りである。定期確認の中でやり直した回では、別のgoroutineで動かし、定期確認は待たない。
    - 持っておいた「mergeのあとに閉じること」をやり直す回は、待たずにIssueを読む。5分が過ぎているためである。閉じる操作の答えが届かなかったときも、読み直しで閉じていると分かる。
    - 読み取りと閉じる操作の10秒の上限が先に来たときも、一時的な失敗と同じく持っておく。閉じる手順でトークンを取れないときは、一時的でない失敗でも持っておく。Ownerに戻す手順もトークンを使うので、Issueを閉じられるのは、あとの回だけだからである。クライアントが読み取りをやり直す間に、上限が来ることがあるためである。
  - mergeが通ったら、10秒待ってから実装Issueを読む (`DefaultCloseWait`)。開いていれば、`cumin-core` が完了として閉じる。GitHubはリンクしたIssueを数秒で閉じていた (実測 60) が、2026-09-30から閉じないことがある (#276 の C5)。待つ時間は設定の表にないので、コードに置く。
  - 待っている間にcuminを止める合図が来たら、待ちを打ち切って、すぐに読んで閉じる。この2つの操作は、止める合図で取り消さず、10秒の上限で行う。上限は、止めるときに実行中の依頼を待つ時間より短くし、プロセスが終わる前に済むようにする。失敗したときにOwnerに戻す手順は、止める合図のもとでは、ほかの道と同じく動かない。止める合図のもとでは、手順を持っておくこともしない。mergeは取り消せず、そのPull Requestはもう開いていないので、あとの定期確認はこのIssueに戻ってこないためである。
  - 閉じるのは、この手順の中だけである。読み取りか閉じる操作が一時的でない失敗で終わったら、Ownerに戻す。手順が終わったあとの定期確認では閉じないので、Ownerが開き直したIssueは開いたままになる。
  - mergeのあと、実装Issueのラベルは替えない。Issueが閉じれば、定期確認の対象から外れる。
- 採らなかった案: mergeの前に `mergeable` を読み、衝突なら呼ばない。読んだ値が古く、衝突を見落とす (#286 の M4)。呼んでから読むほうが、1回の読み取りで確かに分かる。

### Ownerの承認のあとのmerge (I12)

- 定期確認の判定 (純粋関数) が、候補を集める。`cumin/status/awaiting-merge-decision` の開いた実装Issueで、Pull Requestの今の先頭のコミットに、人 (botでないアカウント) の `APPROVED` のレビューがあるものである。実行中のIssueは除く。候補には、判断のレビュー (`APPROVED` か `CHANGES_REQUESTED`) を出した人を全て入れる。
- 候補ごとに、その人たちの権限を `cumin-core` で読む (`GET /repos/{owner}/{repo}/collaborators/{username}/permission`。公式: Get repository permissions for a user。要る権限は Metadata の read。実測は #286 の M1)。`permission` が `admin` か `write` で、`user.type` が `User` の人がOwnerである ([cumin本体の要件](../requirements/cumin-core.md) の「Owner」。maintainは `write` として返る)。候補がなければ、権限も必須のcheckも読まない。
- Ownerのレビューのうち、最新の判断のレビューが今の先頭のコミットへの `APPROVED` なら、I12が成り立つ (純粋関数 `OwnerApproved`)。古いコミットへの承認、botの承認、Ownerでない人の承認は数えない。あとから出したOwnerの `CHANGES_REQUESTED` は、承認を取り消す。それが今の先頭のコミットへのレビューなら、I13が成り立つ (「Ownerのレビューへの対応の依頼 (I13)」)。
- 「最新のレビュー」に、`COMMENTED` は数えない。GitHubも、mergeの判断には `APPROVED` と `CHANGES_REQUESTED` だけを使う。Ownerが承認のあとに質問のコメントを書いても、承認は残る。
- 次に、I6と同じ `DecideMerge` で、riskのラベルと必須のcheckを確かめる。riskのラベルがちょうど1つでなければ、行の番号I12でOwnerに戻す。checkが通っていなければ、何もしない。通れば、次の定期確認でI12がまた成り立つ。riskの値では分けない。Ownerが判断したからである。
- mergeは、「mergeの手順 (I6、I7)」と同じ手順を、行の番号I12で通る。衝突、失敗、閉じ方も同じである。手順は別のgoroutineで動き、実行中のIssueとして数える。手順が実装Issueを閉じるのを待つ間に、次の定期確認が同じIssueを候補にしないためである。一時的な失敗で手順を持っておく間も、実行中のIssueのままなので、I12の候補にならない。やり直すのは、持っておいた手順である。
- 採らなかった案: Ownerの一覧をHostの設定に持つ。要求のbacklogにある。権限はGitHubにあり、設定と二重に持たないほうがよい。

### 上限での原因の整理 (I8)

- 上限のラウンドで `REQUEST_CHANGES` が出たら、そのラウンドのReviewerのセッションのまま、「原因の整理」を依頼する。作業場所は、そのラウンドのworktreeのままである。依頼文には、リポジトリ、実装Issue、Pull Request、上限、作業場所と、Pull RequestにOwner向けのコメントを1つ書き、レビューは出さないという短い指示を入れる。形式はskill `cumin-decision-request` にある。
- 実行が `done` で終わったら、Pull Requestのコメントを、最後のレビューの時刻まで遡って読む (「要求Issueのコメントの読み取り」と同じ問い合わせ)。ReviewerのAppのbotが書き、1行目が `## Decision needed` で始まり、最後のレビューより古くないコメントがあれば、それが原因の整理である (純粋関数 `ExplanationOf`)。比べる時刻はどちらもGitHubの時刻なので、Hostの時計はずれてもよい。
- 見つかれば、ラベルを `cumin/status/awaiting-decision` に替え、Ownerに1回だけ通知する。通知のリンクは、そのコメントのアドレスである。理由はReviewerが書いたので、cuminはコメントを書かない。ラベルを替えられなくても、通知は出す (「Ownerに戻す道」と同じ考え方)。
- 見つからないとき、`blocked` のとき、2回目の異常終了のときは、行の番号I8で「Ownerに戻す道」の手順を呼ぶ。どの場合も、Ownerが決めることに変わりはないためである。`blocked` のコメントは、Reviewerの `blocked_reason` である。
- I8は実行の終わりが決める動作なので、Reviewerの実行ごとに1回しか起きない。コメントと通知が二重になることはない。
- 採らなかった案: 原因の整理が見つからないとき、Reviewerにもう一度依頼する。I8の表は「うまくいかないとき」を定めていない。上限に達した時点で、Ownerが決めることは決まっているので、stop noteで知らせれば足りる。

### 実行終了の判定

![I2の判定](poll-verify.svg)

図の元ファイル: [poll-verify.puml](poll-verify.puml)

- Implementerの実行が `done` で終わったら、cuminはその実行のIssueだけを読み直し ([cumin本体の設計メモ](cumin-core.md) の「GitHubクライアント」)、その実行のブランチの開いているPull Requestを読み、実行したIssueについてI2の3つの確認を、この順で行う。そのブランチに、ImplementerのAppの開いているPull Requestがあること。そのPull Requestの作成者が、ImplementerのAppのbot (`<slug>[bot]`) であること。Pull Requestの先頭のコミット (`headRefOid`) が、worktreeの先頭のコミットと同じであること (最後のコミットがpushされている)。
- Pull Requestは、その実行のブランチ (cuminが決めて依頼に渡したもの) と作成者で見つける。RESTの `GET /repos/{owner}/{repo}/pulls?state=open&head=<owner>:<branch>` で読む (公式: List pull requests。要る権限は Pull requests の read。Permissions required for GitHub Apps)。`head` に持ち主を付けるので、forkのブランチは入らない。ImplementerのAppのものが2つ以上あれば、番号の大きいものを確かめる。ほかのブランチのPull Requestは、Issueにリンクされていても使わない。
- リンクで探さないのは、GitHubが本文の `Closes #N` からリンクを作らないことがあるためである (2026-09-30から。sandboxと他のリポジトリで確かめた)。ほかの行は、今までどおりリンク (`closedByPullRequestsReferences`) でPull Requestを見つける。I2がリンクを付けるので、ほかの行も同じPull Requestを見る。
- 3つの確認が通り、IssueにそのPull Requestを閉じるリンクがなければ、`cumin-core` がGraphQLの `addCloseIssueReferences` でリンクを付ける (公式: GraphQL reference の Issues。入力は `issueId` と `pullRequestIds`)。`cumin-core` のtokenで呼べることは、sandboxで実測した (#276)。付けたあとでそのIssueをもう一度読み直し、リンクがあることを確かめてから、ラベルを替える。GitHubが既にリンクを作っていれば、何も付けない。
- Issueを閉じる開いているPull Requestが、定期確認で読む上限 (2件) に既に達しているときは、リンクを付けずにOwnerに戻す。もう1つ付けると、そのIssueを読めなくなり、そのリポジトリの定期確認が毎回失敗するためである。
- リンクを付けられなかったとき、または読み直してもリンクがないときは、Ownerに戻す。どちらも実行の終わりに1回しか起きないので、やり直さない。
- GitHubの呼び出しが、クライアントのやり直しのあとも一時的な失敗 (`github.IsTemporary`) で終わったときは、この手順を捨てずに持っておく (`keptstep.go`。要件: [cumin本体の要件](../requirements/cumin-core.md) の「GitHubの呼び出しの失敗」)。図の戻る矢印がこれである。
  - 対象は、tokenの発行、Issueの読み直し、ブランチのPull Requestの一覧、リンクを付けたあとの読み直し、ラベルの付け替えである。一時的でない失敗は、今までどおりに扱う。検証が落ちればOwnerに戻し、読めなければラベルを替えずにログに出す。
  - 持っておくものは、リポジトリ、Issueの番号、手順 (関数)、次に試す時刻である。`Service` のメモリの中だけにあり、cuminを再起動すると失われる。
  - 次に試す時刻は、持っておいた時刻の5分後 (固定の値) である。時計は `Service.Now` を使う。定期確認は、リポジトリを読む前に、時刻が来た手順を動かす。それより前の定期確認は、その手順を動かさない。
  - 手順は、最初 (tokenの発行とIssueの読み直し) からやり直す。読み直したIssueに `cumin/status/implementing` がもうなければ、ラベルの付け替えは済んでいるので、何も書かずに終える。答えが届かなかった書き込みを二重にしないためである。
  - また一時的な失敗で終わったら、さらに5分待つ。レート制限のリセットの時刻より前は、クライアントが呼び出しを送らずに失敗を返すので、手順は同じようにさらに5分待つ。成功するか、一時的でない結果になるまで続ける。
  - 持っておく間、Issueは作業中のIssueの集合 (`markInProgress`) に残る。ラベルは `cumin/status/implementing` のままで、進行中の数に数えられ、このIssueのAgentは起動しない。`cumin stop --after-current-runs` は、持っておいた手順が終わるまで待つ。
  - 持っておくときに、warnのログを1行出す。理由と、次に試す時刻を入れる。
  - Reviewerの実行のあとの手順 (I5〜I8、I10) も、同じ仕組みで持っておく (「Reviewerへの依頼 (I3、I10)」)。R2の手順も同じように持っておく (下の「Plannerの実行のあとの手順」)。mergeの手順 (I6、I12) も、同じ仕組みで持っておく (「mergeの手順 (I6、I7)」)。
- 採らなかった案: 全ての行で、ブランチの名前でPull Requestを見つける。スナップショット、I9、mergeのあとのIssueの閉じ方まで変わる。I2でリンクを付ければ、変わるのはI2だけで、GitHubがリンクを作るようになっても、そのまま動く。
- 判定は純粋関数で、結果を値として返す。通ったかどうかと、落ちたときはどの確認で落ちたか (開いているPull Requestがない、作成者が違う、先頭のコミットがpushされていない) と、確かめたPull Requestの番号と、リンクを付けるかどうかである。通れば、リンクを付けてから、ラベルを `cumin/status/checking` に替える。落ちたときは、次の話題の手順でOwnerに戻す。
- 判定に渡す3つの値は、Agentの実行の側から来る。ブランチは依頼に渡したものである。ImplementerのAppのbotのlogin (`<slug>[bot]`) は実行の結果に付いて返り、worktreeの先頭のコミットは `git rev-parse HEAD` で読む ([Agentの実行の設計](agent-run.md) の「作業場所」と「1回の依頼の手順」)。
- 実行終了のあとの読み直しは、1つのIssueを番号で指定する問い合わせである (`ReadSubIssue`、`ReadRequirementIssue`)。リポジトリの全ページは読まない。R2、I2、I5〜I8、I10の判定が使うのは、1つのIssueの事実だけだからである。
  - 実装Issueでは、ラベル、blocked by、そのIssueを閉じる開いているPull Request (check、レビュー、先頭のコミット、`mergeable`)、親の要求Issueの状態とラベル、既定のブランチの名前を読む。要求Issueでは、ラベル、blocked by、sub-issueを読む。sub-issueの項目は、定期確認と同じである。
  - Issueの項目は、定期確認の問い合わせと同じ2つのfragment (`requirementIssueFields`、`subIssueFields`) と、2つ目の問い合わせと同じfragment (`closingPullRequestFields`) から作る。上限も同じ値を渡す。そのため、どちらで読んでも、判定は同じ事実を受け取る。
  - 上限を超えたIssueは、定期確認と同じく、Issueの番号を入れたエラーにする。読めなければ、ラベルを替えずにログに出す。I2と、Reviewerの実行のあと (I5〜I8、I10) と、R2の読み直しが一時的な失敗で終わったときは、手順を持っておく (R2は下の「Plannerの実行のあとの手順」)。
  - 読むのは1回の問い合わせなので、判定が見る事実の時点は1つのままである。
  - ポイントは、実装Issueで1、要求Issueで2である (cumin-worksで実測、2026-10-03、`rateLimit.cost`、[#454](https://github.com/cloveclovedev/cumin-works/pull/454))。全ページを読み直すと、cumin-worksでは34ポイントだった ([#421](https://github.com/cloveclovedev/cumin-works/pull/421))。
  - 読み直しがIssueを返すのは、定期確認がそのIssueを読むときだけである (原則6: 閉じた要求Issueと、そのsub-issueは読まない)。要求Issueは、開いていて、`cumin/type/requirement` のラベルを持つこと。実装Issueは、親がそのような要求Issueであること。そのために、実装Issueの問い合わせは、親の状態とラベルも読む (`parent`)。
  - そうでないIssueは、理由を入れたエラーにして、読めなかったときと同じに扱う: ラベルを替えず、mergeもせず、ログに出す。実行中に要求Issueが閉じられたときも、定期確認が動かないIssueを、実行終了の判定が動かさない。どの行も、定期確認と同じ事実から同じ動作を決める。
- `blocked` の結果と異常終了は、この判定に入らない。`blocked` は次の話題の手順でOwnerに戻す。異常終了は、同じ依頼を1回だけやり直してから、次の話題の手順でOwnerに戻す。
- Plannerの実行が `done` で終わったら、同じようにその要求Issueだけを、sub-issueと一緒に読み直し、R2の2つの確認を行う。sub-issueが1つ以上あること。全てのsub-issueに `risk/*` のラベルがちょうど1つ付いていること。閉じたsub-issueも数える。sub-issueは番号の小さい順に確かめ、最初に落ちたものの番号を結果に入れる。分割の中身は判定しない。見るのはOwnerである。
- R2が通れば、開いているsub-issueがあるときは、要求Issueのラベルを `cumin/status/awaiting-plan-review` に替え、Ownerに「分割結果の確認が必要」と通知する。sub-issueが全て閉じているとき (受け入れの確認が `blocked` で止まったあとに、Ownerが `cumin/status/ready` で再開し、Plannerが何も作らなかったとき) は、`cumin/status/implementing` に替え、通知しない。次の定期確認でR4が成り立つ。行き先を決めるのは純粋関数 (`SplitStatus`) である。通知のリンクは要求Issueのアドレスである。通知は止まったことの知らせではないので、戻す道の手順を通らず、同じ通知の部分を直接呼ぶ。
- Plannerの `blocked` と異常終了は、I2と同じ扱いで、行の番号をR2にしてOwnerに戻す。Plannerのセッションは手元に残さない。
- Plannerの実行のあとの手順: 分割の実行のあとの手順も、GitHubの呼び出しが一時的な失敗 (`github.IsTemporary`) で終わったときは、I2と同じ仕組み (`keptstep.go`) で持っておく (要件: [cumin本体の要件](../requirements/cumin-core.md) の「GitHubの呼び出しの失敗」)。次の図の戻る矢印がこれである。


  ![R2の判定](poll-split.svg)

  図の元ファイル: [poll-split.puml](poll-split.puml)

  - `done` のあとの対象は、tokenの発行、要求Issueの読み直し、ラベルの付け替えである。`blocked` のあとの対象は、Ownerに戻す前の、tokenの発行と要求Issueの読み直しである。Ownerに戻す手順の中の失敗は、今までどおり、ログに出して次に進む。
  - 持っておくもの、次に試す時刻 (5分後)、ログ、作業中のIssueの集合に残ることは、I2と同じである。要求Issueのラベルは `cumin/status/planning` のままで、進行中の数に数えられ、このIssueのPlannerは起動しない。
  - 手順は、要求Issueの読み直しからやり直す。検証が落ちれば、今までどおりOwnerに戻す。
  - `done` のあとのやり直しで読み直した要求Issueに `cumin/status/planning` がもうなければ、ラベルを替えない。前の回のラベルの付け替えが一時的な失敗で終わっていて、そのラベルが要求Issueに付いているなら、付け替えは届いている。そのラベルが `cumin/status/awaiting-plan-review` なら、通知だけを出す。通知はラベルの付け替えのあとに出すので、まだ出ていないためである。`cumin/status/implementing` なら、何もしない。それ以外のとき (前の回が読み直しで失敗していた、または別のラベルが付いている) は、cumin以外がラベルを替えたので、何もせず、通知もしない。
  - `blocked` のあとのやり直しは、要求Issueのラベルが何であっても、Ownerに戻す手順を行う。持っておくのは読み直しが失敗したときだけで、読み直しはOwnerに戻す手順のどの書き込みよりも前にある。そのため、やり直しの時点で、コメントも通知もまだ出ていない。Plannerの `blocked_reason` を失わないためである。
  - 持っておかないもの: 異常終了のあとの読み直しと、受け入れの確認 (R4) の実行のあとの手順。R4は、要求Issueが `cumin/status/accepting` のまま残り、次の定期確認が事実から決める。

### うまくいかなかったときに、Ownerに戻す道

- 先に進めないときは、1か所の手順でOwnerに戻す。行の番号 (I2、R2 など) を引数で受け取り、順に、止まったIssue (I2では実装Issue、R2では要求Issue) にコメントを書き、状態ラベルを `cumin/status/awaiting-decision` に替え、Ownerに通知する。I4、I15 (必須のcheckが結果を返さない)、I3 (Reviewerの2回目の異常終了)、I5 (Reviewerのレビューが2回とも見つからない)、I10 (Reviewerの `blocked`) も、同じ手順を自分の行の番号で呼ぶ。あとの行 (I8) も同じである。
- 順番に意味がある。理由がGitHubに残ってからラベルが替わり、最後に「見に来てほしい」と伝える。
- 途中で1つ失敗しても、次を止めない。コメントを書けなくてもラベルは替え、ラベルを替えられなくても通知は出す。巻き戻しもしない。止まったIssueがあることは、どれか1つが落ちても伝わるほうがよい。失敗はログに出す。
- 通知のリンクは、書いたコメントのアドレスにする。理由の全文がそこにあるためである。コメントを書けなかったときは、Issueのアドレスにする。
- 検証が落ちたときのコメントは、cuminが [stop-note.md](../../../templates/stop-note.md) の形式で書く。本文には、落ちた確認の1文 (I2では、ブランチに開いているPull Requestがない、作成者が違う、先頭のコミットがpushされていない、リンクの数が上限に達している、リンクを付けられなかった (GitHubの答えを入れる)、読み直してもリンクがない。R2では、sub-issueがない、あるsub-issueにriskのラベルがない、2つ以上ある) と、確かめたPull Requestの番号 (R2では「None」) を入れる。同じ1文を通知にも入れて、Ownerがどちらを読んでも同じ言葉になるようにする。
- `blocked` のときのコメントは、Agentが返した `blocked_reason` をそのまま載せる。Agentが [decision-request.md](../../../templates/decision-request.md) の形式で書いているためである。通知には、その1行目 (Ownerに決めてほしいこと) を入れる。やり直さない (Issueのラベルと状態遷移の、Implementerが `blocked` を返したときの決まり)。
- ラベルを替えるには、そのIssueの今のラベルが要る。`blocked` の道では、実行終了のあとにそのIssueを読み直して取る。読み取れなければ、ラベルを替えずにログに出す。状態ラベルだけを書き込むと、riskのラベルが消えるためである。
- 通知を出すかどうかは、そのリポジトリの設定 `notify.discord.enabled` で決まる。通知の失敗は error のログに出すだけである ([cumin本体の設計メモ](cumin-core.md) の「Ownerへの通知」)。
- 異常終了のときは、同じ依頼を同じ作業場所で1回だけやり直す ([Agentの実行の設計](agent-run.md) の「異常終了のやり直し」)。2回目も異常終了なら、この手順でOwnerに戻す。コメントには、異常終了の種類と、やり直したことを書く。Agentが残したPull Requestがあれば、その番号も書く。作業がGitHubまで届いたかどうかを、Ownerが先に知れるためである。

### 定期確認が続けて失敗したとき

- リポジトリごとに、続けて失敗した回数と、その理由を数える。3回目に1回だけOwnerに知らせ、そのリポジトリの定期確認が成功するまで、それ以上は送らない。理由が変われば別の問題なので、数え直す。ただし、一度知らせたあとは、理由が変わっても次の通知は出さない。次に知らせるのは、成功を挟んだあとである (cumin本体の要件の通知の表)。
- 理由が同じかどうかは、失敗の文章が同じかどうかで見る。文章にはファイルの名前とキーの名前が入るので、同じ誤りなら同じ文章になる。
- 通知には行の番号を入れない。cumin本体の要件の通知の表で、この行にだけ番号がないためである。リンクはリポジトリにする。Issueの問題ではないからである。
- 通知を出すかどうかは、Hostの設定で決める。リポジトリの設定 (`notify.discord.enabled`) は、そのリポジトリのIssueについての通知に効く。定期確認そのものが失敗しているときは、リポジトリの設定を読めていないか、その誤りが原因であることがあるので、リポジトリの側には決めさせない。
- 二重に送らないことを、何で保証するか。実行終了をきっかけにする通知 (`blocked`、検証の失敗、異常終了) は、GitHub上の事実で保証する。1回の実行の終わりに1回だけ通り、そのときラベルが `cumin/status/awaiting-decision` に替わるので、同じ実行で二度は起きない。定期確認の失敗の数は、GitHub上に事実がないので、cuminがメモリで数える。
- メモリで足りる理由。cuminが再起動すると数は0に戻るが、失敗が続いていれば3回の定期確認 (初期値で3分) のあとに改めて知らせる。遅れるだけで、失われない。要件が手元に持ってよいと認めたものの一覧 (cumin本体の要件の「状態の持ち方」) に、この数は入っていないので、ファイルにはしない。
- 採らなかった案: 失敗のたびに知らせる。定期確認は60秒ごとなので、直らない誤りが通知の洪水になる。

## まだ決めていないこと

なし。

## 後回しにしたこと

- 要求の水準で後回しにしたことは、[要求のbacklog](../requirements/backlog.md) にある。
