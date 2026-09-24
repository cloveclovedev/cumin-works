# GitHubに残す文章のテンプレート

OwnerとAgentがGitHubに残す文章の型を定める。テンプレートそのものは英語で、リポジトリの直下の `templates/` にある。この文書は、それぞれの狙いと、決めた理由を説明する。

GitHubに残す文章は、平易な英語で書く。読み手は、英語を母語としないOwnerと、他のAgentである。

## テンプレートの一覧

| テンプレート | 書く人 | 読む人 | 使う場面 |
|---|---|---|---|
| [writing-rules.md](../../../../templates/writing-rules.md) | 全てのAgent | — | 平易な英語の決まり。全ての文章に当てはまる |
| [requirement-issue.md](../../../../templates/requirement-issue.md) | Owner | Planner | 要求Issueを書く |
| [implementation-issue.md](../../../../templates/implementation-issue.md) | Planner | Implementer、Reviewer | 実装Issueを作る |
| [plan-summary.md](../../../../templates/plan-summary.md) | Planner | Owner | 要求Issueに、分割の全体像をコメントする |
| [acceptance-check.md](../../../../templates/acceptance-check.md) | Planner | Owner | sub-issueが全て閉じたあとに、要求が満たされているかを確かめた結果を、要求Issueにコメントする |
| [pull-request.md](../../../../templates/pull-request.md) | Implementer | Reviewer、Owner | Pull Requestの説明を書く |
| [review.md](../../../../templates/review.md) | Reviewer | Implementer、Owner | レビューの指摘と、レビューのまとめを書く |
| [review-reply.md](../../../../templates/review-reply.md) | Implementer | Reviewer、Owner | レビューの指摘に返答する |
| [decision-request.md](../../../../templates/decision-request.md) | 全てのAgent | Owner | Ownerに判断を求める。`blocked` のときと、3ラウンドで指摘が残ったとき |
| [follow-up-note.md](../../../../templates/follow-up-note.md) | cumin (Agentではない) | Owner | mergeのあとに、残った作業をフォローアップノートとして要求Issueに転記する |

## どのテンプレートにも共通の決まり

- 見出しは、テンプレートの通りに書く。見出しは、AgentとcuminとOwnerの間の約束である。
- 書くことがない節は、消さずに「None」と書く。
- 1つの実装Issueが、1つのPull Requestになる。
- 太字は使わない。

## テンプレートごとの狙い

### 要求Issue

- Ownerが書くのは「何が欲しいか」と「なぜか」である。作り方は、制約でない限り書かない。大きなOSSの機能要望のテンプレートも、人が書く欄はこの2つに絞っている。
- 要求は、正しいか正しくないかを確かめられる決まりとして、1行に1つ書く。
- 1つの要求Issueの大きさは、[要求Issueの分割基準](requirement-sizing.md) に従う。
- 決めていないことは「Open questions」に書く。Plannerは、分割が変わるような未決事項に当たったら、推測せずに `blocked` を返す。
- 目的の1文には、「When (状況)、I want (したいこと)、so I can (得たい結果)」の型を使ってもよい。利用者の人物像を決めなくても書ける。

### 実装Issue

- 本文だけで作業できるようにする。Implementerは、本文、リンクされた文書、リポジトリから作業する。あとから付いたコメントには頼らない。
- 背景、範囲、完了条件、関連する文書の4つの節に、2つの節を足した。「Pointers」(変更するファイルと、手本にする既存のコード) と、「How to verify」(確かめるためのコマンド) である。GitHub Copilot、Claude Code、Codexの公式の手引きが、そろってこの2つを求めている。
- 依存関係は、本文ではなく、GitHubの blocked by の関係で記録する。同じことを2か所に書かない。

### 分割の全体像

- Ownerが、実装Issueを1つずつ開かなくても、分割の全体をつかめるようにする。
- 「Requirement coverage」で、要求の1つ1つが、どの実装Issueで満たされるかを示す。抜けがあれば、ここで見つかる。
- 「Assumptions」に、要求に書かれていなかったのでPlannerが決めたことを書く。Ownerは、作業が始まる前に直せる。
- 承認の方法は、返信ではなく、実装Issueに `cumin/status/ready` を付けることである。

### 受け入れの確認

- Ownerが、自分で確かめ直さなくても、受け入れるかどうかを決められるようにする。
- 要求Issueの Requirements の1項目を、表の1行にする。要求と結果が、1対1で対応する。
- 証拠の欄に、実行したコマンドと結果を書かせる。確かめ方の間違いで、不具合があるように見えることがある。証拠があれば、Ownerが切り分けられる。
- Failがあっても、Plannerは直さない。Issueも作らない。どう直すかの提案を書く。差し戻すかどうかは、Ownerが決める。提案があれば、Ownerは差し戻しのsub-issueを、提案を写して書ける。
- 見出しの `## Acceptance check` は、cuminが確認の済んだことを判定するのに使う。

### Pull Requestの説明

- 題はConventional Commitsの形にし、命令形で書く。
- 「How it was tested」には、実行したコマンドと結果を貼る。「テストは通った」と書くだけにしない。
- 「Documentation」は、文書を直したか、直す必要がないかを、必ず書かせる。

### レビュー

- 指摘の形式は Conventional Comments に従う。全ての指摘に、ラベルを1つと、`(blocking)` か `(non-blocking)` のどちらかを必ず付ける。
- `(blocking)` が1つでもあれば `REQUEST_CHANGES`、1つもなければ `APPROVE` にする。指摘の重さとレビューの結果が、機械的に対応する。
- `(blocking)` にしてよい理由を、7つに限った。Reviewerの要件の「修正が必要な指摘にしてよいもの」と同じである。好みの問題は、常に `(non-blocking)` にする。こうしないと、ラウンドを重ねても指摘がなくならない。
- 指摘には、必ず「Why」を書く。理由のない指摘は、Implementerが直しようがない。
- 指摘は、書いた人ではなく、コードについて書く。

### レビューの指摘への返答

- 返答は、`Fixed`、`Not changed`、`Deferred`、`Answer` のどれかで始める。
- 同意しないときは、事実 (テストの結果、文書、相手の案の具体的な問題) を示してから、質問を1つする。
- 同じ反論を繰り返さない。3ラウンドで決着しなければ、Ownerが決める。
- コメントのスレッドを解決済みにする操作は、v0.1ではどのAgentも行わない。cuminは、レビューの結果だけで判定する。

### Ownerに判断を求める

- 1行目に、決めてほしいことを書く。Ownerが、残りを読まなくても問いが分かるようにする。
- 「Not decided」に、何が決まっていないのか、それをどこに書くべきかを書く。3ラウンドで指摘が残るのは、多くの場合、要求の側に決まっていないことがあるためである。
- 選択肢を2つか3つ、良い点と悪い点を付けて示し、1つを勧める。
- 続け方も書く。Ownerは、コメントで答えるかIssueを直してから、実装Issueに `cumin/status/ready` を付ける。
- Agentが `blocked` を返すときは、この形式の文章を `blocked_reason` に入れる。cuminが、それをIssueのコメントとして投稿する。

### フォローアップノート

- Implementerが `Follow-up` に書いた範囲の外の作業と、対応されなかった `(non-blocking)` の指摘は、mergeされるとPull Requestの中に埋もれる。cuminがこれを、フォローアップノートとして要求Issueに転記する。
- cuminはAIの判断を使わない。機械的に拾えるのは、指摘に必ず `(blocking)` か `(non-blocking)` が付き、返答が `Fixed` などの決まった言葉で始まるからである。テンプレートの形式は、このためにも守らせる。
- フォローアップノートは記録であり、Issueにはしない。Ownerがやりたいものを新しい要求Issueに書けば、通常のフローで実装Issueになる。

## 平易な英語の決まり

[writing-rules.md](../../../../templates/writing-rules.md) の10項目。文の長さの上限 (指示は20語、説明は25語) は、Simplified Technical Englishという規格の数字である。他の項目は、Google、Microsoft、米国政府の、英語を母語としない読み手に向けた文章の手引きに共通している。

## 置き場所

| テンプレート | 置き場所 |
|---|---|
| 要求Issue | 対象のリポジトリの `.github/ISSUE_TEMPLATE/` に置くと、OwnerがGitHubの画面でIssueを作るときに使える |
| Pull Requestの説明 | 対象のリポジトリの `.github/pull_request_template.md` に置いてもよい。ただし、AgentがAPIでPull Requestを作るときには使われない (実測 52) ので、cuminがskillとして渡す |
| それ以外 | cuminが、依頼のたびに、roleとしての指示と一緒にskillとして渡す。テンプレートごとに1つのskillにし、Agentはその文章を書く直前にskillを読む。skillはroleごとのディレクトリに置き、そのroleが書く文章のものだけを渡す。roleが書かない文章のskillを見せると、そのroleがしてはいけない行動を誘うためである。平易な英語の決まりだけは、全ての文章に当てはまるので、指示の本文に入れる |

GitHubのIssueのテンプレートと入力フォームは、画面でIssueを作るときだけ働く。AgentがAPIでIssueを作るときには働かないので、Agentは自分で見出しを書く。

## 出典

調査は2026-09-19に行った。引用は、ページを要約する取得ツールを通して確かめたものが多い。文言をそのまま使うときは、原文を確かめ直すこと。

| 出典 | 使った箇所 |
|---|---|
| Conventional Comments https://conventionalcomments.org/ | 指摘のラベルと、`(blocking)` `(non-blocking)` |
| Google Engineering Practices (レビューする側) https://google.github.io/eng-practices/review/reviewer/comments.html 、https://google.github.io/eng-practices/review/reviewer/standard.html | 理由を書く。コードについて書く。完璧でなくても、良くなっていれば承認する。決着しないときは上に回す |
| Google Engineering Practices (書く側) https://google.github.io/eng-practices/review/developer/cl-descriptions.html 、https://google.github.io/eng-practices/review/developer/handling-comments.html | 説明の1行目は命令形。同意しないときの書き方 |
| GitHub Copilot coding agent https://docs.github.com/en/copilot/tutorials/coding-agent/get-the-best-results | 実装Issueに要るもの (問題の説明、完了条件、変更するファイル) |
| Claude Code best practices https://code.claude.com/docs/en/best-practices | 実行できる確認手段を渡す。範囲の外を明記する。証拠を示させる。Reviewerには、正しさと要件に関わる点だけを指摘させる |
| OpenAI Codex prompting https://learn.chatgpt.com/docs/prompting | 確かめ方を書く |
| Kubernetesのテンプレート https://github.com/kubernetes/kubernetes/tree/master/.github | 人が書く欄は「何を」と「なぜ」に絞る。レビューする人への注記 |
| Cucumber (Example Mapping、Gherkin) https://cucumber.io/docs/bdd/example-mapping/ | 決まりの一覧として完了条件を書く。未決事項を別に書き出す |
| SBAR https://www.ihi.org/library/tools/sbar-tool-situation-background-assessment-recommendation 、MADR https://adr.github.io/madr/ | 判断を求める文章の順序。選択肢と得失 |
| Google developer documentation style guide https://developers.google.com/style/translation 、Microsoft Style Guide https://learn.microsoft.com/en-us/style-guide/global-communications/writing-tips 、digital.gov https://digital.gov/guides/plain-language/writing 、ASD-STE100 https://www.asd-ste100.org/ | 平易な英語の決まり |
| GitHub Docs (テンプレート、レビュー、Issueを閉じる言葉) https://docs.github.com/en/communities/using-templates-to-encourage-useful-issues-and-pull-requests/about-issue-and-pull-request-templates 、https://docs.github.com/en/rest/pulls/reviews | テンプレートの置き場所。レビューの結果の種類。`REQUEST_CHANGES` には本文が要る |

出典に基づかず、この文書で決めたこと:

- 指摘の「Why」「Fix」という項目名と、ラウンド数の表示
- 返答の4つの書き出しと、直したコミットを書く決まり
- 判断を求める文章の「Not decided」「To continue」「Until then」の行
- 分割の全体像の「Requirement coverage」と、承認の方法
