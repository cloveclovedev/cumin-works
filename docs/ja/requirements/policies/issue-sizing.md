# 実装Issueの分割基準

Plannerが要求Issueを実装Issueに分割するときの判断基準。1つの実装Issueは、1つのAgentが1つのPull Requestで実装する。

基準は公開されている指針から組み立てた (2026-09-19調査)。AIが実装しAIがレビューするPull Requestの適切な大きさを直接調べた研究は見つからなかった。数値の目安は人間のレビューのデータからの借り物であり、運用しながら調整する。

## 判断基準

基準1〜9のどれかが「いいえ」なら、分割するか書き直す。基準10は、分割しすぎを止めるための条件。

| # | 基準 | 「いいえ」のとき | 出典 |
|---|---|---|---|
| 1 | 目的が1つ。変更内容を「〜と〜」を使わずに1文で説明できる | 分割する。リファクタリング、機能追加、バグ修正は混ぜない。リファクタリングを先のIssueにする | Google、GitHub、Claude Code |
| 2 | 完了条件を検証できる。Agentがコマンド (テスト、ビルド、lint) を実行して完了を確かめられる。テストと文書の期待も書いてある | 書き直す。これは分割の問題ではなく、仕様の問題 | INVEST、Copilot、Codex、Claude Code、METR |
| 3 | 未決事項がない。進め方が分かっている。要求が曖昧だったり、意味のある設計案が複数あったりしない | 実装Issueにしない。何が決まっていないかを要求Issueに書き、Ownerに戻す | Copilot、Humanizing Work、SPIDR |
| 4 | 単独でmergeできる。このPull Requestだけをmergeしてもmainが壊れない。同じ要求Issueに紐づく他のsub-issueと内容が重ならない。順序の依存は blocked by で明示してある | 分け方を変える。未完成の機能は、フラグで隠す、expand/contractで段階的に入れる、などで単独mergeできる形にする | Wake (INVEST)、Google、DORA、Fowler |
| 5 | 動く機能の単位で切ってある、または下準備だと明記してある。このIssueだけで、外から見て分かる振る舞いの変化がある。そのために必要な層 (画面、API、データベースなど) を、1つのIssueでまとめて変更する。そうでなければ、下準備 (リファクタリングのみ、インターフェースやスタブの追加、expandの段階) だと明記してある | 切り直す。「データベースだけ」「APIだけ」のように層ごとに分けると、他のIssueと合わせるまで動くものができない。下準備だと明記していない、層だけの変更にしない | Humanizing Work、Google、DORA |
| 6 | 大きさが目安に収まる。想定される差分は100〜200行が目標。400行、または10ファイルを超えない | 分割する。1000行は理由の明記がない限り不可。ファイルの丸ごと削除、生成コード、信頼できる機械的なリファクタリングは対象外 | Google、SmartBear/Cisco、Google ICSE 2018、Microsoft Research |
| 7 | 作業量が目安に収まる。事情を知らないエンジニアが数時間から1日で終えられる | 1〜2日を超えるなら分割する | DORA、Wake、trunkbaseddevelopment.com、METR |
| 8 | risk/high の変更が隔離してある。revertで戻せない変更 (DBマイグレーション、デプロイやCIの設定、認証や決済、外部サービスへの副作用、公開APIの契約、cumin自身のルール) に触れるなら、その変更だけの最小のIssueになっている | 分割する。expandとcontractは別のIssueにする。こうすると、同じ要求Issueに紐づく他のsub-issueが risk/low や risk/medium のままでいられる | DORA、Fowler、Copilot、Microsoft Research (組み合わせは推論) |
| 9 | Implementerが変更できないファイルの変更を要しない。保護されたパス (対象のリポジトリの `.cumin/config.toml` の `protected_paths`) と、ImplementerのGitHub Appの権限では書けないファイル (`.github/workflows/` の下) がこれに当たる | その変更を、Ownerが手で行うsub-issueとして作る。`cumin/type/owner-task` と `risk/high` を付け、状態ラベルは付けず、Contextに理由を書く。依存する実装Issueに blocked by を張り、分割の全体像の Please check にOwnerの作業として書く。cuminはこのsub-issueに着手せず、Ownerが閉じるまで依存する実装Issueは止まる | cumin (Implementerの要件と、GitHub Appの権限) |
| 10 | これ以上分割しない条件。分割すると次のどれかになるなら、分割しない: 単独で検証できる結果がなくなる、ロジックとそのテストが別れる、使う側のないAPIだけが入る、同じ要求Issueに紐づく他のsub-issueのPull Requestなしでは意味が分からない、順序の依存が増えるだけ | — | Google、Wake/Patton、Humanizing Work |

## 数値の目安についての注意

- Googleの指針は「厳密な決まりはない」とした上で、100行は妥当、1000行は大きすぎる、としている。「1ファイル200行は良くても、50ファイルに散った200行は大きすぎる」とも述べている。
- 200〜400行という数字は、2006年のCisco 1チームの観察研究 (ツールベンダーのSmartBearが実施) に由来する。人間のレビュアーは300〜400行を超えると欠陥の発見率が落ちた、という結果であり、AIのレビュアーで検証されたものではない。
- 「400行または10ファイル」という線は、上記から組み立てた目安であり、どの出典にもそのまま書かれてはいない。
- DORAは、AIが生成したコードは人間が書いたコードより1行あたりのレビュー負荷が高いかもしれない、と注意している。
- METRの計測では、Agentの成功率は人間換算の作業時間が長いほど、また課題が入り組んでいるほど下がる。80%の確率で成功する作業時間は、50%の確率で成功する作業時間の数分の1になる。ただしMETR自身が「X時間以下の作業をAIに任せられるという意味ではない」と断っている。
- METRの別の調査では、テストに通ったAgentの成果物が、そのままではmergeできない品質だった (テスト、文書、lintの不足)。基準2で完了条件にテストと文書の期待まで書くのはこのため。

## 出典

確度の凡例: 照合済み = 原文を取得して引用を突き合わせた、抽出のみ = 取得ツールの抽出結果で確認した (サイトが直接取得を拒否したため、原文との突き合わせはできていない)。

| 出典 | URL | 確度 |
|---|---|---|
| Google Engineering Practices "Small CLs" | https://google.github.io/eng-practices/review/developer/small-cls.html | 照合済み |
| SmartBear/Cisco コードレビュー事例研究 | https://static0.smartbear.co/support/media/resources/cc/book/code-review-cisco-case-study.pdf | 照合済み |
| Google "Modern Code Review" (ICSE-SEIP 2018) | https://sback.it/publications/icse2018seip.pdf | 照合済み |
| Microsoft Research "Characteristics of Useful Code Reviews" (MSR 2015) | https://www.microsoft.com/en-us/research/wp-content/uploads/2016/02/bosu2015useful.pdf | 照合済み |
| DORA "Working in small batches" | https://dora.dev/capabilities/working-in-small-batches/ | 照合済み |
| DORA "Trunk-based development" | https://dora.dev/capabilities/trunk-based-development/ | 照合済み |
| DORA "Streamlining change approval" | https://dora.dev/capabilities/streamlining-change-approval/ | 抽出のみ |
| DORA "Database change management" | https://dora.dev/capabilities/database-change-management/ | 抽出のみ |
| GitHub Copilot coding agent "Get the best results" | https://docs.github.com/en/copilot/tutorials/coding-agent/get-the-best-results | 照合済み |
| GitHub "Helping others review your changes" | https://docs.github.com/en/pull-requests/collaborating-with-pull-requests/getting-started/helping-others-review-your-changes | 抽出のみ |
| Claude Code best practices | https://code.claude.com/docs/en/best-practices | 照合済み |
| OpenAI Codex best practices / prompting | https://learn.chatgpt.com/guides/best-practices | 抽出のみ |
| METR time horizons | https://metr.org/time-horizons/ | 照合済み |
| METR 長い作業の計測 (2025-03) | https://metr.org/blog/2025-03-19-measuring-ai-ability-to-complete-long-tasks/ | 抽出のみ |
| METR 成果物の品質の調査 (2025-08) | https://metr.org/blog/2025-08-12-research-update-towards-reconciling-slowdown-with-time-horizons/ | 抽出のみ |
| Bill Wake "INVEST in Good Stories" ほか | https://xp123.com/articles/invest-in-good-stories-and-smart-tasks/ | 抽出のみ |
| Humanizing Work ストーリー分割ガイド | https://www.humanizingwork.com/the-humanizing-work-guide-to-splitting-user-stories/ | 抽出のみ |
| Mike Cohn "SPIDR" | https://www.mountaingoatsoftware.com/blog/five-simple-but-powerful-ways-to-split-user-stories | 照合済み |
| Martin Fowler "Parallel Change" / "Evolutionary Database Design" | https://martinfowler.com/bliki/ParallelChange.html | 抽出のみ |
| trunkbaseddevelopment.com "Short-lived feature branches" | https://trunkbaseddevelopment.com/short-lived-feature-branches/ | 抽出のみ |
