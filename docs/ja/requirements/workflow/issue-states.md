# Issueのラベルと状態遷移

[メインワークフロー](main-workflow.puml) に出てくる要求Issueと実装Issueについて、状態を表すラベルと、状態が移る条件をまとめる。状態が移る条件は、cuminの動作ごとに「何をきっかけに動くか」と「動く前に何を確かめるか」として書く。

cuminの動作のきっかけは、この文書の表を正とする。[cumin本体の要件](../cumin-core.md) と各Agentの要件は、この表の番号を参照する。

## ラベルの一覧

ラベルには4つの種類がある。優先度のラベルのほかは、名前の前半で見分けられる。

- `cumin/type/*`: Issueの種類を表す。`cumin/type/requirement` は要求Issue、`cumin/type/owner-task` はOwnerが手で行う作業のIssueを表す。状態ではないので、閉じるまで付けたままにする。
- `cumin/status/*`: Issueの状態を表す。1つのIssueに常に1つだけ付く。
- `risk/*`: mergeのriskを表す。実装Issueに常に1つだけ付く。
- 優先度のラベル: 着手の順番を表す。名前と順番は、リポジトリの設定で決まる ([cumin本体の要件](../cumin-core.md) の「設定」)。設定がなければ、`cumin/priority/P0` 〜 `cumin/priority/P3` である。付けなくてもよい。決まりは下の「着手の順番」にある。

したがって要求Issueには、`cumin/type/requirement` と `cumin/status/*` の2つが付く。

| ラベル | 付く対象 | 意味 | 付ける人 |
|---|---|---|---|
| `cumin/type/requirement` | 要求Issue | これは要求Issueである | Owner |
| `cumin/type/owner-task` | 実装Issue | この作業はOwnerが手で行う。cuminは着手しない。Implementerが変更できないファイル (保護されたパス、Appの権限で書けないファイル) の変更が要るときに使う | Planner、Owner |
| `cumin/status/ready` | 実装Issue、要求Issue | Ownerが「進めてよい」と合図した | Owner |
| `cumin/status/planning` | 要求Issue | Plannerが分割している | cumin |
| `cumin/status/implementing` | 実装Issue、要求Issue | 実装Issueでは、Implementerが動いている (修正を含む)。要求Issueでは、sub-issueの実装が進んでいる | cumin |
| `cumin/status/awaiting-checks` | 実装Issue | Implementerの実行が終わり、必須のcheckの完了を待っている。Agentは動いていない | cumin |
| `cumin/status/reviewing` | 実装Issue | Reviewerが作業している | cumin |
| `cumin/status/awaiting-owner-review` | 実装Issue、要求Issue | Ownerが見て承認するのを待っている。実装Issueでは、Pull RequestのmergeをOwnerが判断する。OwnerがGitHubのレビューで承認すると、cuminがmergeする (I12)。要求Issueでは、分割結果と受け入れ | cumin |
| `cumin/status/awaiting-owner-decision` | 実装Issue、要求Issue | Agentが先に進めない。Ownerの回答を待っている | cumin |
| `risk/low`、`risk/medium`、`risk/high` | 実装Issue | mergeのrisk。Plannerが仮に付け、Ownerが確定する | Planner、Owner |
| 優先度のラベル (初期値は `cumin/priority/P0` 〜 `cumin/priority/P3`) | 要求Issue、実装Issue | 先に着手してほしい順番。`P0` が最も高い | Owner |

cuminは、実装Issueの `cumin/status/*` と `risk/*` を、そのIssueを閉じるPull Requestにもコピーする (原則5)。

実装Issueであることを表すラベルは作らない。`cumin/type/requirement` の付いたIssueのsub-issueが、実装Issueである。

## 原則

1. きっかけは2種類だけ。cuminがGitHubを定期的に確認して見つけた状態 (定期確認) と、cuminが起動したAgentの実行が終わったこと (実行終了) である。Agentのプロセスはcuminの子なので、終了はcuminにすぐ分かる。
2. Agentの申告では判定しない。GitHub上の事実で判定する。Agentには実行の最後に結果 (`done` か `blocked` か、と理由) を決まった形式で返させるが、`done` は「確かめ始めてよい」という合図でしかない。Pull Requestがあるか、checkが通ったか、レビューが出たかは、cuminがGitHubで確かめる。
3. 着手は、依頼より先にラベルを付け替えることで表す。同じIssueを二重に依頼しない。
4. 状態を表すラベルは1つのIssueに常に1つだけで、付け替えは全てcuminが行う。例外は `cumin/status/ready` で、これだけはOwnerが付ける。
5. 判定に使うのは、Issueのラベルだけである。cuminは、実装Issueの状態とriskのラベルを、そのIssueを閉じるPull Requestにもコピーする (I11)。コピーしたラベルは、OwnerがPull Requestの一覧で状態とriskを見分けるためのもので、cuminは読まない。Pull Requestの側でラベルを変えても、cuminがIssueのラベルで上書きする。Ownerの合図 (`cumin/status/ready`) は、常にIssueに付ける。
6. cuminは、閉じた要求Issueには何もしない。読まず、ラベルを替えず、コメントも書かない。

Agentの結果を決まった形式で受け取る手段として、Claude Codeのheadless実行には `--json-schema` がある (公式ドキュメントで確認済み)。

## 要求Issueの状態遷移

![要求Issueの状態遷移](requirement-issue-states.svg)

図の元ファイル: [requirement-issue-states.puml](requirement-issue-states.puml)

| # | cuminの動作 | きっかけ | 動く前に確かめること | うまくいかないとき |
|---|---|---|---|---|
| R1 | 要求Issueに `cumin/status/planning` を付け、Plannerに分割を依頼する | 定期確認: 開いていて、`cumin/type/requirement` と `cumin/status/ready` が付いた要求Issueがある。sub-issueがあるかどうかは問わない | 最新の `cumin/status/ready` を付けたのがOwnerである (下の「Ownerのready」)。要求Issueの blocked by のIssueが全て閉じている。AIリソースに空きがある | — |
| R2 | 要求Issueのラベルを `cumin/status/awaiting-owner-review` に替え、Ownerに「分割結果の確認が必要」と通知する。sub-issueが全て閉じているときは、`cumin/status/implementing` に替え、通知しない | 実行終了: Plannerの実行が終わった | sub-issueが1つ以上ある。全てのsub-issueにriskのラベルがちょうど1つ付いている | `cumin/status/awaiting-owner-decision` に替えて通知する。異常終了なら、その前に1回だけやり直す |
| R3 | 要求Issueのラベルを `cumin/status/implementing` に替える | 定期確認: 要求Issueに `cumin/status/awaiting-owner-review` が付いたあとで、sub-issueのどれかに `cumin/status/ready` が付いた。要求Issueに状態ラベルがないときは、sub-issueのどれかに `cumin/status/ready` が付いていればよい | — | — |
| R4 | Plannerに、受け入れの確認を依頼する。ラベルは `cumin/status/implementing` のままにする | 定期確認: sub-issueが全て閉じていて、最後のsub-issueが閉じたあとに書かれた受け入れの確認のコメントが、まだない | 要求Issueに `cumin/status/implementing` が付いている。sub-issueが1つ以上ある。閉じたsub-issueのフォローアップノート (I9) を書き終えている。この要求IssueのAgentが動いていない。AIリソースに空きがある | 結果が `blocked` なら、`cumin/status/awaiting-owner-decision` に替えて通知する。異常終了なら1回だけやり直し、それでも駄目なら同じ扱いにする |
| R5 | (何もしない。完了) | Ownerが要求Issueを閉じた | — | — |
| R6 | 要求Issueのラベルを `cumin/status/awaiting-owner-review` に替え、Ownerに「残りのsub-issueの確認が必要」と通知する | 定期確認: 開いているsub-issueが1つ以上あり、その全てに状態ラベルがない | 要求Issueに `cumin/status/implementing` が付いている | — |
| R7 | 要求Issueのラベルを `cumin/status/awaiting-owner-review` に替え、Ownerに「受け入れ可能になった」と通知する | 定期確認: sub-issueが全て閉じていて、最後のsub-issueが閉じたあとに書かれた受け入れの確認のコメントがある | 要求Issueに `cumin/status/implementing` が付いている | — |

差し戻しのとき、Ownerはsub-issueを追加して `cumin/status/ready` を付ける。これでR3が再び成り立ち、追加分が閉じるとR4が再び成り立つ。前の受け入れの確認のコメントは、追加分が閉じるより前に書かれたものなので、Plannerがもう一度確かめる。

受け入れの確認は、sub-issueを全て合わせたmainの上で、要求Issueの Requirements が満たされているかを、Plannerが1項目ずつ確かめることである。Pull Requestは1つずつしか検証されないので、全体を確かめる人がほかにいない。分割が正しいことは、Ownerが分割結果の確認で見ている。受け入れの確認は、その分割を前提にして、まとめた結果が1つの振る舞いとして正しいかを見る。

- 受け入れの確認のコメントとは、PlannerのGitHub Appが要求Issueに書いた、`## Acceptance check` で始まるコメントである。形式は [acceptance-check.md](../../../../templates/acceptance-check.md) に従う
- cuminは、コメントがあるかどうかだけを見る。表の結果 (Pass か Fail か) は読まない。Failがあっても、R7で `cumin/status/awaiting-owner-review` に替える。差し戻すかどうかは、Ownerが決める
- R4では、ラベルを付け替えない。受け入れの確認が済んだかどうかは、コメントの有無というGitHub上の事実で分かるためである。cuminが途中で止まっても、コメントがなければR4が、あればR7が、次の定期確認で成り立つ
- R6では、受け入れの確認をしない。sub-issueが全て閉じたときにだけ行う

R3が `cumin/status/ready` の付いた時刻を見るのは、要求Issueを見直す場面のためである。前の分割で `cumin/status/ready` が付いたsub-issueは、cuminが着手するまでそのラベルのまま残る。ラベルの有無だけで判定すると、Ownerが新しいsub-issueを確認する前に、要求Issueが `cumin/status/implementing` に替わってしまう。ラベルが付いた時刻は、GitHubがIssueのイベントとして記録している。

`cumin/type/owner-task` の付いたsub-issueは、Ownerが作業を済ませてから閉じる。cuminは、`cumin/status/ready` が付いていても着手しない。それに依存する実装Issueは、blocked by で止まる。Ownerが閉じ忘れて他のsub-issueが全て閉じると、R6が成り立つ。

Ownerは、分割結果の確認のとき、一部のsub-issueにだけ `cumin/status/ready` を付けてもよい。それらが全て閉じて、状態ラベルのないsub-issueだけが残ると、R6が成り立ち、cuminがもう一度Ownerに確認を求める。Ownerが残りを忘れて、要求Issueが黙って止まることを防ぐ。残りのsub-issueが要らなくなったときは、Ownerがそれを閉じる。全て閉じれば、R4が成り立つ。

Ownerは、要求Issueを書き終えたら `cumin/status/ready` を付ける。これでR1が成り立つ。分割に失敗して `cumin/status/awaiting-owner-decision` になったときも、要求Issueを直してから `cumin/status/ready` を付ける。sub-issueが途中まで作られていても、Plannerは既にあるsub-issueを確かめて、同じものを二重に作らない。`cumin/type/requirement` は要求Issueである印なので、外さずに付けたままにする。Ownerの「進めてよい」の合図を、実装Issueと同じ `cumin/status/ready` に揃えるため、この形にしている。

受け入れの確認 (R4) が `blocked` を返して `cumin/status/awaiting-owner-decision` になったときも、Ownerは答えをコメントに書き、要求Issueに `cumin/status/ready` を付ける。R1が成り立ち、Plannerが要求Issueを読み直す。Requirementsを書き足していれば、Plannerは足りない分のsub-issueを作り、R2で分割結果の確認に進む。作るものがなければ、sub-issueは全て閉じたままなので、R2は要求Issueを `cumin/status/implementing` に替え、通知しない。次の定期確認でR4が成り立ち、Plannerがもう一度受け入れを確かめる。要求Issueの `cumin/status/ready` は、いつも「Plannerが要求を読み直す」という1つの意味である。

要求Issueが他の要求Issueに依存するときは、Ownerが要求Issueどうしに blocked by を張る。先の要求Issueが閉じるまで、R1は成り立たない。先の要求の成果がまだ入っていないmainを読んで、Plannerが分割してしまうことを防ぐ。実装Issueの blocked by は、同じ要求Issueのsub-issueの間だけに張る。

OwnerがPlannerを通さずに、自分でsub-issueを書いてもよい。このときOwnerは、要求Issueには `cumin/status/ready` を付けず、sub-issueにだけ付ける。R1は成り立たず、R3が成り立つ。

## 実装Issueの状態遷移

![実装Issueの状態遷移](implementation-issue-states.svg)

図の元ファイル: [implementation-issue-states.puml](implementation-issue-states.puml)

| # | cuminの動作 | きっかけ | 動く前に確かめること | うまくいかないとき |
|---|---|---|---|---|
| I1 | ラベルを `cumin/status/implementing` に替え、新しいセッションでImplementerに実装を依頼する。Pull Requestが既にあれば、続きから進めるよう依頼する | 定期確認: 開いていて `cumin/status/ready` が付いた実装Issueがある | `cumin/type/owner-task` が付いていない。最新の `cumin/status/ready` を付けたのがOwnerである (下の「Ownerのready」)。blocked by のIssueが全て閉じている。AIリソースに空きがある (下の「上限の決め方」で、どちらの枠も上限未満) | — |
| I2 | IssueにそのPull Requestを閉じるリンクがなければ、`cumin-core` がリンクを付け、付いたことを読み直して確かめる。そのあとラベルを `cumin/status/awaiting-checks` に替え、必須のcheckの完了を待ち始める | 実行終了: Implementerの実行が終わり、結果が `done` | cuminがこのIssueのために決めたブランチに、Pull Requestが開いている。そのPull Requestの作成者が、ImplementerのGitHub Appである。ブランチの先頭のコミットがpushされている | 結果が `blocked`、Pull Requestがない、作成者が違う、先頭のコミットがpushされていない、またはリンクを付けられなかったなら `cumin/status/awaiting-owner-decision` に替えて通知する。異常終了なら1回だけやり直し、それでも駄目なら同じ扱いにする |
| I3 | ラベルを `cumin/status/reviewing` に替え、Reviewerにレビューを依頼する | 定期確認: `cumin/status/awaiting-checks` の実装Issueで、必須のcheckが、Pull Requestの先頭のコミットで全て通った。必須のcheckが1つもなければ、すぐに通ったとみなす | — | — |
| I4 | ラベルを `cumin/status/implementing` に戻し、失敗したcheckの内容を添えてImplementerに修正を依頼する | 定期確認: `cumin/status/awaiting-checks` の実装Issueで、必須のcheckのどれかが失敗した | checkの修正依頼が上限 (3回) に達していない | 上限に達したら `cumin/status/awaiting-owner-decision` に替えて通知する |
| I5 | ラベルを `cumin/status/implementing` に替え、Implementerに指摘の修正を依頼する | 実行終了: Reviewerの実行が終わった | Reviewerの最新のレビューが、Pull Requestの今の先頭のコミットに対する `REQUEST_CHANGES` である。ラウンドが上限に達していない | レビューが出ていない、または古いコミットに対するものなら、Reviewerに1回だけ依頼し直す |
| I6 | Pull Requestをmergeする。GitHubが実装Issueを閉じなければ、少し待ってから1回だけ閉じる | 実行終了: Reviewerの実行が終わった | 最新のレビューが今の先頭のコミットに対する `APPROVE` である。checkが全て通っている。riskが `risk/low` である | 衝突でmergeできないなら、`cumin/status/implementing` に替えて、同じセッションでImplementerに解消を依頼する。riskのラベルがちょうど1つでないとき、または衝突のほかの理由でmergeできないときは、mergeせずに `cumin/status/awaiting-owner-decision` に替えて通知する。mergeのあとで実装Issueを閉じられなかったときも、`cumin/status/awaiting-owner-decision` に替えて通知する。閉じるのはOwnerである |
| I7 | ラベルを `cumin/status/awaiting-owner-review` に替え、Ownerに「mergeの判断が必要」と通知する | I6と同じ | I6と同じ。ただしriskが `risk/medium` または `risk/high` である | — |
| I8 | Reviewerに「何が決まっていないことが原因か」の整理を依頼し、レポートが投稿されたら `cumin/status/awaiting-owner-decision` に替えて通知する | 実行終了: Reviewerの実行が終わった | 最新のレビューが `REQUEST_CHANGES` で、ラウンドが上限に達した | — |
| I9 | 残った作業を、フォローアップノートとして要求Issueに転記する。実装Issueはこれで完了 | 定期確認: 実装Issueが閉じていて、それを閉じるよう結び付いたPull Requestが、merge済みである。誰が閉じたか (GitHub、cumin、Owner) は問わない | このPull Requestのフォローアップノートが、まだない。要求Issueが開いている。`Follow-up` に文章があるか、対応されなかった `(non-blocking)` の指摘がある | — |
| I10 | `blocked_reason` を実装Issueにコメントとして投稿し、ラベルを `cumin/status/awaiting-owner-decision` に替えて通知する。やり直さない | 実行終了: Reviewerの実行が終わり、結果が `blocked` | — | — |
| I11 | Pull Requestの `cumin/status/*` と `risk/*` のラベルを、実装Issueと同じにする | 定期確認: 実装Issueを閉じる開いているPull Requestのラベルが、実装Issueと違う | — | — |
| I12 | Pull Requestをmergeする。mergeの手順と、うまくいかないときの扱いは、I6と同じ | 定期確認: `cumin/status/awaiting-owner-review` の開いた実装Issueで、Owner ([cumin本体の要件](../cumin-core.md) の「Owner」) が出したレビューのうち最新のものが、Pull Requestの今の先頭のコミットに対する `APPROVE` である | 必須のcheckが、その先頭のコミットで全て通っている。riskのラベルがちょうど1つである | I6と同じ |
| I13 | ラベルを `cumin/status/implementing` に替え、Implementerの直前のセッションで、Ownerのレビューへの対応を依頼する | 定期確認: `cumin/status/awaiting-owner-review` の開いた実装Issueで、Owner ([cumin本体の要件](../cumin-core.md) の「Owner」) が出したレビューのうち最新のもの (コメントだけのレビューは除く) が、Pull Requestの今の先頭のコミットに対する `REQUEST_CHANGES` である。そのレビューは、実装Issueに最新の `cumin/status/awaiting-owner-review` が付いたあとに出されている | — | 異常終了なら1回だけやり直し、それでも駄目なら `cumin/status/awaiting-owner-decision` に替えて通知する |
| I14 | ラベルを `cumin/status/implementing` に替え、Implementerの直前のセッションで、衝突の解消を依頼する。依頼はI6の衝突の解消と同じである | 定期確認: `cumin/status/awaiting-checks` または `cumin/status/awaiting-owner-review` の開いた実装Issueで、GitHubがPull Requestを既定のブランチと衝突していると返した (GraphQLの `mergeable` が `CONFLICTING`) | — | 解消のあとも先頭のコミットが変わらなければ、`cumin/status/awaiting-owner-decision` に替えて通知する。異常終了なら1回だけやり直し、それでも駄目なら同じ扱いにする |
| I15 | ラベルを `cumin/status/awaiting-owner-decision` に替え、Ownerに「必須のcheckが結果を返さない」と通知する。通知には、Pull Requestの先頭のコミット、まだ結果を返していない必須のcheck、待った時間を書く。開いているPull Requestがないときは、そのことと待った時間を書く | 定期確認: `cumin/status/awaiting-checks` の開いた実装Issueで、checkの待ち時間 ([cumin本体の要件](../cumin-core.md) の「設定」) を過ぎても、必須のcheckのどれかが、先頭のコミットで結果を返していない。実装Issueを閉じる開いているPull Requestがない (誰かが閉じたなど) ときも、待ち時間を過ぎたら成り立つ | I14、I3、I4のどれも成り立たない | — |

Ownerのready:

- `cumin/status/ready` はOwnerの「進めてよい」の合図である。R1とI1は、そのIssueに最新の `cumin/status/ready` を付けたのが、Owner ([cumin本体の要件](../cumin-core.md) の「Owner」) であるときだけ成り立つ。GitHubでは、triageの権限でもラベルを付けられるためである。承認のあとのmerge (I12) と同じく、作業を始めさせられるのもOwnerだけにする
- Ownerでないアカウントが付けた `cumin/status/ready` には、着手しない。ラベルは替えない。ログに1回だけ残し、Ownerに1回だけ通知する。同じreadyについて、定期確認のたびに繰り返さない
- そのIssueは、Ownerがreadyを付け直すまで進まないので、待ち状態の通知 (Q4) では「Ownerなしで進めるIssue」に数えない

I5〜I8は、Reviewerの結果が `done` のときの動作である。結果が `blocked` のときは、I10に従う。

I7のあと、OwnerはPull RequestをGitHubのレビューで判断する。

- 承認するときは、今の先頭のコミットに `APPROVE` のレビューを出す。I12が成り立ち、cuminがmergeする。古いコミットへの承認は数えない。承認のあとにOwnerが `REQUEST_CHANGES` を出すと、最新のレビューが承認でなくなるので、mergeしない。衝突の解消などで新しいコミットがpushされたら、Ownerはもう一度承認する
- Ownerの判断を待つ間に、ほかのPull Requestのmergeで衝突したら、Ownerが承認する前に、I14がImplementerに解消させる。解消のあと、I2、必須のcheck、Reviewerのレビュー (I3) を通り、I7でもう一度Ownerの判断を待つ。Ownerは、mergeできる先頭のコミットだけを判断すればよい
- 差し戻すときは、今の先頭のコミットに `REQUEST_CHANGES` のレビューを出す。I13が成り立ち、cuminがImplementerに直させる。Implementerが `done` を返すと、I2、必須のcheck、Reviewerのレビュー (I3) を通り、I7でもう一度Ownerの判断を待つ。Reviewerのレビューのラウンドは、Reviewerの最後の `APPROVE` のあとから数え直すので、差し戻しのあとは1ラウンド目から始まる
- 1つの `REQUEST_CHANGES` で差し戻すのは1回だけである。Ownerが質問だけをして、Implementerがコミットせずに答えると、先頭のコミットは変わらず、Ownerの `REQUEST_CHANGES` はそのコミットに残る。I13は、実装Issueが最後に `cumin/status/awaiting-owner-review` になったあとのレビューだけで成り立つので、Issueはそのまま、Ownerの判断に戻る。Ownerは答えを読んで、承認するか、もう一度 `REQUEST_CHANGES` を出す
- コメントを書いて実装Issueに `cumin/status/ready` を付けて差し戻すこともできる。I1が成り立ち、続きの依頼になる
- Ownerのうちadminのアカウントは、自分でmergeしてもよい。mainのrulesetを迂回できるのは、adminと `cumin-core` だけだからである ([セットアップの手順](../../development/setup-guide.md))。そのあとの扱いは、cuminがmergeしたときと同じである (I9)。ただし、GitHubが実装Issueを閉じなければ、Ownerが閉じる。cuminが閉じるのは、自分のmergeの直後だけである

mergeのあとにcuminが実装Issueを閉じるのは、GitHubの動作に合わせるためである。2026-09-30から、GitHubは、手で付けたリンクや `addCloseIssueReferences` で付けたリンクのPull Requestをmergeしても、Issueを閉じないことがある。cuminは、mergeの手順の中で少し待ってから実装Issueを読み、開いていれば1回だけ閉じる。あとの定期確認では閉じないので、Ownerが開き直したIssueは開いたままになる。mergeと閉じる操作の間でcuminが止まったときも、あとで閉じ直さない。そのIssueは、下の「v0.1では実装しないこと」の辻褄の合わないIssueと同じく、Ownerが閉じる。

必須のcheckとは、mainに適用されるrulesetの "Require status checks to pass before merging" に登録されたcheckである。cuminは `GET /repos/{owner}/{repo}/rules/branches/{branch}` でその一覧を読む。

- 一覧が空なら、checkを待たずに進む。
- checkは、結論が `success`、`skipped`、`neutral` のどれかのとき、通ったとみなす。GitHubも、この3つをmergeを止めない結論として扱う。条件で飛ばされたjob (例えば、人が作ったPull Requestでの、保護されたパスのcheck) は、`skipped` になる。
- 一覧が空でなければ、その全てがPull Requestの先頭のコミットで通るまで待つ。
- 「今そのコミットに付いているcheckの一覧」で判定しないのは、pushの直後はcheckがまだ1つも現れておらず、「checkがない」のか「これから現れる」のかを区別できないためである。必須のcheckの一覧は、pushの前から決まっている。

checkを待つ間の行 (I3、I4、I14、I15):

- 1つの定期確認では、I14、I3とI4、I15の順に判定し、最初に成り立った行だけを動かす
- I14は、`mergeable` が `CONFLICTING` のときだけ成り立つ。GitHubは、Pull Requestに衝突があると `pull_request` のワークフローを動かさないので、衝突したままではcheckがいつまでも結果を返さない。`UNKNOWN` は、GitHubがまだ計算している印である。既定のブランチに何かがmergeされるたびに、開いているPull Requestはしばらく `UNKNOWN` になる。そのため、`UNKNOWN` ではその定期確認でI14を判定せず、Ownerにも回さない。`UNKNOWN` のまま待ち時間を過ぎたら、I15が成り立つ
- I14は、checkの修正を依頼した回数に数えない。衝突はImplementerの誤りではなく、並行して進むほかのPull Requestのmergeで起きるためである
- I15の待ち時間は、実装Issueに最新の `cumin/status/awaiting-checks` が付いた時刻と、先頭のコミットの時刻 (`committedDate`) の、遅いほうから数える。ふつうはI2が先頭のコミットのpushを確かめてからラベルを替えるので、ラベルの時刻になる。先頭のコミットの時刻が効くのは、待っている間に誰かがブランチにpushしたときである。GitHubはpushの時刻を返さない (GraphQLの `Commit.pushedDate` は使えない) ので、コミットの時刻で代える。コミットの時刻はpushより前なので、そのぶん早く止まりうるが、差はコミットからpushまでの間だけである
- 開いているPull Requestがないときは、先頭のコミットがないので、待ち時間はラベルの時刻から数える。checkが来ないのと同じく、どの行もそのIssueを進めないためである
- I15は、checkが結果を返さない理由を調べない。ワークフローの誤り、どのワークフローも報告しない必須のcheckの名前、無効にしたワークフロー、GitHub Actionsの障害などがある。cuminは見える事実だけを書き、理由はOwnerが調べる

`cumin/status/awaiting-checks` を置くのは、「Implementerの実行が終わり、checkを待っている」ことをGitHubに残すためである。`cumin/status/implementing` のままだと、Implementerが修正の途中なのか、checkを待っているのかを、GitHub上の事実から区別できない。必須のcheckが1つもないリポジトリでも、この状態を必ず通る。次の定期確認で、すぐにI3が成り立つ。

I2だけは、Pull Requestをブランチと作成者で見つける。GitHubが本文の `Closes #N` からリンクを作らないことがあるためである (2026-09-30に確かめた)。I2がリンクを付けるので、ほかの行は、IssueとPull Requestのリンクで、そのIssueのPull Requestを見つける。GitHubが既にリンクを作っていれば、cuminは何も付けない。

I2〜I4により、「Pull Requestが開かれた」ことは実装完了のきっかけにならない。Implementerの実行が終わり、かつcheckが全て通ったことをcuminが確かめて、初めてレビューに進む。checkの待ち時間にAgentは動いていないので、利用枠を消費しない。

Ownerが `cumin/status/awaiting-owner-review` や `cumin/status/awaiting-owner-decision` のIssueに `cumin/status/ready` を付け直すと、I1が再び成り立つ。cuminは着手のときに、古い状態ラベルを外す。

レビューのラウンドと、checkの修正を依頼した回数は、I1で `cumin/status/ready` から `cumin/status/implementing` に移るたびに0に戻る。Ownerが介入したあとはAgentのセッションも新しくなるので、上限も数え直す。レビューのラウンドは、次の2つのうち新しいほうよりあとに出た、`cumin-reviewer` のレビューの数で数える。実装Issueに最後に `cumin/status/ready` が付いたときと、`cumin-reviewer` が最後に `APPROVE` を出したときである。`APPROVE` のあとで数え直すのは、I6で衝突を解消すると先頭のコミットが変わり、レビューをやり直すことになるためである。どちらもGitHub上の事実なので、cuminは数を手元に持たない。

Implementerが `blocked` を返したとき (I2) は、やり直さずに、1回目から `cumin/status/awaiting-owner-decision` に替える。実装に必要な要件が足りない、という場合が多いためである。Ownerは実装Issueか、その上の要求Issueを書き直して進められる状態にしてから、実装Issueに `cumin/status/ready` を付け直す。Reviewerが `blocked` を返したとき (I10) も、同じ扱いにする。

## 着手の順番

Agentを起動する着手 (R1、R4、I1) は、同時に進めるIssueの数の上限を分け合う。空きより多くの着手が成り立つときは、次の順に着手する。

1. 優先度の高い順。優先度は、優先度のラベルで表す。ラベルの名前と、高い順の並びは、リポジトリの設定で決まる。
2. 同じ優先度なら、Issueの番号の小さい順。

- 優先度のラベルがないIssueは、どの優先度のラベルが付いたIssueよりもあとに着手する。ラベルのないIssueどうしは、番号の小さい順である。
- 優先度のラベルがない実装Issueは、その要求Issueの優先度を引き継ぐ。Ownerは、要求Issueに1回付ければよい。実装Issueに優先度のラベルがあれば、そちらを使う。
- 1つのIssueに優先度のラベルが2つ以上付いているときは、高いほうを使う。
- 優先度のラベルを付けるのは、Ownerである。cuminとAgentは、付けも外しもしない。Pull Requestにもコピーしない。
- 優先度は、着手できるIssueの間の順番だけを決める。blocked by のIssueが開いているIssue、同時に進めるIssueの数の上限、利用枠の上限を越えて着手することはない。実行中のAgentを、優先度の高いIssueのために止めることもない。

## Agentのセッションの扱い

- Ownerが介入したあと (`cumin/status/ready` の付け直し) は、Agentのセッションを新しくする。新しいセッションのAgentは、Issue、Pull Request、レビュー、Ownerのコメントを GitHub から読み直して、続きから進める。
- Ownerの介入を挟まない一続きの作業 (I4のcheckの修正、I5の指摘の修正、Reviewerの2ラウンド目以降) は、同じセッションで続ける。
- Ownerのレビューへの対応 (I13) も、Implementerの直前のセッションで続ける。ほぼ出来上がったPull Requestへのコメントを直すことが多く、Issueが書き直されたわけではないので、前の文脈がそのまま役に立つためである
- こうする理由は3つある。書き直される前のIssueを前提にした古い文脈を引きずらない。作業の状態はGitHubにあるので、セッションを捨てても失うものがない。Ownerの対応には時間が空くので、古いセッションを再開すると長い文脈を読み直す分だけ利用枠を余計に使う。

## Issueの状態によらないトリガー

利用枠と待ち状態に関するcuminの動作。

| # | cuminの動作 | きっかけ | 動く前に確かめること |
|---|---|---|---|
| Q1 | 新しい着手 (R1、I1) を止め、Ownerに通知する。実行中のIssueは最後まで進める | 実行終了、または着手の直前の確認: 5h枠の使用率が今のしきい値以上になった。weekly枠の使用率がペースの上限以上になった。または、着手の直前の確認で使用率を読み取れなかった | 止めてから再開するまでに、この枠について (読み取れなかったときは、そのことについて) まだ通知していない |
| Q2 | 着手を再開する | Host上のコマンドで、Ownerが5h枠の使い切りを許可した。許可はその5h枠のリセットまで有効 | weekly枠の使用率がペースの上限未満である |
| Q3 | 着手を再開する | 定期確認: 手元に残した使用率から決めた、次に試す時刻を過ぎた | 着手の直前の確認で、どちらの枠も上限未満である |
| Q4 | Ownerに「待ち状態になった」と通知する | 定期確認: Ownerが動かなければ何も進まない。次の全てが成り立つときである。実行中のAgentがいない。R1もI1も成り立たない (利用枠だけで止まっている着手 (Q1) は、成り立つものとして数える。Q1が原因を知らせているためである)。cuminがOwnerなしで次に進めるIssueがない (下の表) | 前回の通知のあとに、cuminが何か動作をした (同じ通知を繰り返さない) |

Q4で「cuminがOwnerなしで次に進めるIssue」に数えるかどうか:

| Issueの状態 | 数えるか | 理由 |
|---|---|---|
| `cumin/status/awaiting-checks` | 数える。Q4を出さない | 必須のcheckが終われば、cuminがI3かI4で進める。衝突すればI14で、checkの待ち時間を過ぎればI15で進める。待ち時間の間はQ4を出さない。そのIssueは `cumin status` に見える |
| `cumin/status/ready` で、着手できるのに、同時に進めるIssueの数の上限だけで待っている | 数える。Q4を出さない | 空きができれば、cuminがR1かI1で着手する |
| `cumin/status/ready` で、blocked by のIssueが開いている | 数えない | 前のIssueが閉じるまで動けない。前のIssueがcuminの作業中なら、そちらがQ4を止める |
| `cumin/status/ready` で、利用枠だけで止まっている | (Q1に任せる) | 上の行のとおり、R1とI1が成り立つものとして数える |
| `cumin/status/planning`、`cumin/status/implementing`、`cumin/status/reviewing` | 実行中のAgentがいれば、Q4を出さない | Agentがいないまま残ったもの (cuminの再起動のあとなど) は、Ownerが `cumin/status/ready` を付け直すまで進まないので、数えない |
| `cumin/status/awaiting-owner-review`、`cumin/status/awaiting-owner-decision` | 数えない | Ownerの判断を待っている |
| `cumin/type/owner-task`、状態ラベルのないIssue | 数えない | Ownerが動くまで進まない |

使用率の読み方:

- 使用率は、Agentの実行結果から読む。加えて、着手 (R1、I1) の直前にも読み直す。同じアカウントの利用枠は、cuminの外 (Ownerの作業など) でも使われるためである。
- 着手でない依頼 (I4、I5、Reviewerへの依頼など) の前には、読み直さない。上限に達していても、実行中のIssueは最後まで進めるためである。
- 読んだ使用率は、枠ごとのリセット時刻と、読んだ時刻とともに、Hostの状態ファイルに残す。
- 止めている間は、残した使用率から、次に着手を試みる時刻を決める。その時刻までは、使用率を読むためだけの実行をしない。使用率はリセットまで下がらないので、その時刻より前に上限を下回ることはない。
  - 5h枠が止めているときは、5h枠のリセット時刻と、しきい値が残した使用率を上回る時間帯の始まりのうち、早いほうである。
  - weekly枠が止めているときは、ペースの上限が残した使用率を上回る時刻と、weekly枠のリセット時刻のうち、早いほうである。
  - 両方の枠が止めているときは、両方の時刻のうち、遅いほうである。
- 着手の直前に使用率を読み取れなければ、着手せずに、Ownerに通知する。次の定期確認で、読み直す。

上限の決め方:

- 5h枠の上限は、時間帯ごとのしきい値である。Ownerが使わない時間帯は高くする。どの時間帯にも入らない時刻には、初期のしきい値を使う。5h枠は、今の時間帯にOwnerの分を残すために使う。
- weekly枠の上限は、ペースの上限 = 目標 × min(1, (経過時間 + 前倒し) ÷ 7日) である。経過時間は、weekly枠のリセット時刻の7日前からの時間である。リセット時刻は使用率を読むたびに受け取り、設定には持たない。weekly枠に時間帯はない。
- 使い切りを許可できるのは5h枠だけである。許可があると、その5h枠のリセットまで、5h枠のしきい値を100%とする。weekly枠のペースの上限は、Ownerの許可でも上がらない。
- しきい値、時間帯、目標、前倒しは、cuminの設定ファイルで変えられる。コードに埋め込まない。

## Agentの異常終了

Agentの実行が異常終了したとき (プロセスの失敗、タイムアウトなど) は、同じ依頼を1回だけやり直す。それでも駄目なら `cumin/status/awaiting-owner-decision` に替えてOwnerに通知する。利用枠の上限に当たった場合はやり直しに数えず、枠のリセットを待つ。

Agentの実行のあとの手順 (R2、I2、I5〜I8、I10、Reviewerの承認のあとのmerge) で、GitHubの呼び出しが一時的な理由で失敗したときは、Agentの異常終了ではない。手順を持っておき、あとの定期確認でやり直す ([cumin本体の要件](../cumin-core.md) の「GitHubの呼び出しの失敗」)。

## v0.1では実装しないこと

- 辻褄の合わないIssueの回収。cuminが再起動すると、`cumin/status/planning`、`cumin/status/implementing`、`cumin/status/reviewing` のまま、実行中のAgentがいないIssueが残りうる。`cumin/status/awaiting-checks` のIssueは、Agentが動いていない状態なので、再起動のあともI3、I4、I14、I15で続きから進む。将来は、ラベルとcuminの動作状態を突き合わせて、適切な状態まで戻す機能を作る。v0.1では、OwnerがそのIssueに `cumin/status/ready` を付け直せば、I1により続きから再開する。
- mergeの前にmainの最新を取り込んでcheckをやり直すこと (I6、I12)。v0.1では、衝突がなく、実装時点の必須のcheckが通っていればmergeする。将来は、rulesetの "Require branches to be up to date before merging" を使う案がある。依存関係のあるIssueは、先のIssueがmergeされてから着手するので、この問題が起きるのは並行して進めた独立のIssueの間だけである。

## まだ確かめていないこと

- `GET /repos/{owner}/{repo}/rules/branches/{branch}` は、公式ドキュメント (permissions-required-for-github-apps) によれば、GitHub Appのtokenで呼べて、要る権限は Metadata: Read-only である。実機では、まだ確かめていない。
