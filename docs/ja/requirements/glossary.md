# 用語集

cumin-worksの文書で使う語の意味を決める。「使わない言い方」にある言い方は、文書では使わない。「英語」は、GitHubに残す文章やコードで使う訳である。

語の書き方は次のとおりとする。

- 人と役目の名前（Maintainer、Plannerなど）と、GitHubの機能の名前（Issue、Pull Request、sub-issue、GitHub App）は、英語のまま書く。
- それ以外の一般の語は、日本語かカタカナで書く（マージ、チェック、レビュー、リスク、ラベル）。
- 状態遷移の語は、JSTQBのFoundation Levelシラバス（v4.0）の状態遷移テストの語に合わせる。テストの語も、同じシラバスのテストレベルの語に合わせる。
- 和文と、英数字、インラインコード、リンクのあいだに空白を入れない。括弧は全角の（）を使う。

## 人と役目

| 語 | 意味 | 使わない言い方 | 英語 |
|---|---|---|---|
| Maintainer | 対象のリポジトリにwrite以上の権限を持つ、人のアカウント。要求Issueを書き、進めてよいと合図する | メンテナ、Owner | Maintainer |
| Assignee | そのIssueの指示と判断を受け持つMaintainer。GitHubのIssueのassigneeで表し、1つのIssueに1人だけ付ける | Issue Owner、Owner、担当者 | assignee |
| Operator | Hostでcuminを動かす人 | 運用者、管理者 | Operator |
| Agent | cuminが起動して作業させるAI。今はheadlessのClaude Codeである | エージェント、AI、bot | agent |
| role | Agentが受け持つ役目。Planner、Implementer、Reviewerの3つがある | ロール、Agent Role | role |
| Planner | 要求Issueを計画し、受け入れ確認をするrole | | Planner |
| Implementer | 実装Issueを実装し、Pull Requestにまとめるrole | | Implementer |
| Reviewer | Pull Requestをレビューするrole | | Reviewer |

## cuminと動かす場所

| 語 | 意味 | 使わない言い方 | 英語 |
|---|---|---|---|
| cumin-works | このプロダクトの名前 | cumin works v2、v2 | cumin-works |
| cumin | 動いているcumin-works。コマンドの名前でもある | 基盤、エンジン、ワークフロー基盤 | cumin |
| Host | cuminが動くマシン | ホスト、サーバ | Host |
| 対象のリポジトリ | cuminがIssueとPull Requestを扱うGitHubのリポジトリ | 製品リポジトリ | target repository |
| GitHub App | cuminと各roleがGitHubで使う身元。`cumin-core`、`cumin-planner`、`cumin-implementer`、`cumin-reviewer` | botアカウント | GitHub App |
| mainブランチ | 対象のリポジトリの既定のブランチ。名前がmainでなくても、こう呼ぶ | 既定のブランチ、デフォルトブランチ | default branch |
| Agent作業ディレクトリ | Agentが作業する、Issueとroleごとのgit worktree。作業ディレクトリの下に置く。Agentから見えるのはこの中である | ワークスペース、作業場所 | agent work directory |
| 作業ディレクトリ | Hostの上で、すべてのAgent作業ディレクトリを置く親のディレクトリ。設定の`work_dir`で決める | ワークスペース、作業場所、作業ルートディレクトリ | work directory |
| セッション | Agentの会話の続き。続きを頼むときは、同じセッションでAgentを起動することがある | 会話、スレッド | session |

## Issueと状態遷移

| 語 | 意味 | 使わない言い方 | 英語 |
|---|---|---|---|
| 要求 | Maintainerが実現したいこと。要求Issueに書く | | need |
| 要求Issue | 要求を書いたIssue。`cumin/type/requirement`が付く | 要件Issue、親Issue | requirement issue |
| 実装Issue | 要求Issueのsub-issueで、1つのPull Requestで実装する単位 | 子Issue、タスク | implementation issue |
| sub-issue | GitHubの親子関係で、あるIssueの子になっているIssue | サブIssue、子Issue | sub-issue |
| ドラフト | 状態ラベルのないIssue。cuminは扱わない。GitHubのdraftのPull Requestとは別のものである | 下書き | draft |
| 状態 | Issueがワークフローのどこにいるか。状態ラベル1つで表す | ステータス | state |
| 状態ラベル | 状態を表す`cumin/status/*`のラベル | ステータスラベル | status label |
| 遷移 | 状態が移ること。同じ状態にとどまる遷移もある。「イベント[ガード条件] / アクション」の形で書く | | transition |
| イベント | 遷移を始めるきっかけ。書かない遷移は、ガード条件が成り立ったときに、cuminが定期確認かAgentの実行の終わりの直後に起こす | きっかけ、トリガー | event |
| ガード条件 | 遷移が起きるために成り立っている必要がある条件 | 前提 | guard condition |
| アクション | 遷移のときにcuminが行うこと | 動作、処理 | action |
| 定期確認 | cuminが一定の間隔でGitHubを読み、アクションを決めること | ポーリング、巡回 | poll |
| 空き | リポジトリごとに同時に進めるIssueの数（[定期確認](poll/index.md)の`CWK-POLL-005`）に、余裕があること | 枠 | free slot |
| Agent起動 | cuminがroleとIssueを決めてAgentを始め、作業を頼むこと。続きを頼むときも含む | 依頼、指示、起動 | agent start |
| 通知 | Hostの設定で決めた通知先に知らせること。文書には相手の名前を書かない | 連絡、アラート | notify |
| 作業中のリポジトリ | 短い間隔で定期確認する対象のリポジトリ。条件は[定期確認](poll/index.md)の`CWK-POLL-004`で決める | アクティブなリポジトリ | busy repository |
| 全部を読めないIssue | sub-issue、ラベル、blocked byのIssue、結び付いたPull Requestのどれかが、cuminの読む数の上限を超えたIssue。cuminは何もしない | 大きすぎるIssue | issue that cumin cannot read whole |

## 作業の流れ

| 語 | 意味 | 使わない言い方 | 英語 |
|---|---|---|---|
| 計画 | Plannerが要求Issueを読み、実装Issueに分け、依存関係とリスクを付けること | 分割、分解 | plan |
| 計画結果の確認 | 要求IssueのAssigneeが、計画の結果とsub-issueを確かめること | 分割結果の確認、plan review、プランレビュー | plan review |
| リスク | 実装Issueのマージのリスク。`cumin/risk/*`のラベルで表す | risk | risk |
| 保護されたパス | Agentが変えてはいけないファイルのパス | 保護パス | protected path |
| 必須チェック | mainブランチの保護で、マージの前に成功している必要があるチェック | 必須のcheck | required check |
| ラウンド | Reviewerのレビュー1回と、それへの修正の組 | 周回 | round |
| マージ判断 | 実装IssueのAssigneeがPull Requestを見て、マージするかを決めること | mergeの判断、マージ判定 | merge decision |
| マージ判断を待つPull Requestのブランチから始める | blocked byのIssueのPull RequestがAssigneeのマージ判断を待つ間に、そのブランチから新しいブランチを切り、そのブランチをbaseにしたPull Requestで、次の実装Issueを進めること | 土台、積む、重ねる、スタック | start on the branch of a pull request that waits for the merge decision |
| baseがmainブランチでないPull Request | マージ判断を待つPull Requestのブランチから始めた実装IssueのPull Request。blocked byのIssueのPull Requestがマージされるまでマージしない | 積んだPull Request、重ねたPull Request、先のPull Request、子PR | pull request with a base other than the default branch |
| 受け入れ確認 | Plannerが、マージされた結果が要求Issueを満たすかを確かめること | 受け入れの確認、受入確認 | acceptance check |
| 受け入れ | 要求IssueのAssigneeが受け入れ確認を見て、要求Issueを閉じること | 承認、完了確認 | acceptance |
| 差し戻し | 要求IssueのAssigneeが受け入れずに、sub-issueを足して進め直すこと | 却下、やり直し | send back |
| フォローアップノート | マージのあとに残った指摘を、cuminが要求Issueにまとめて書くコメント | Follow-upコメント | follow-up note |

## 利用制限とツール

| 語 | 意味 | 使わない言い方 | 英語 |
|---|---|---|---|
| 利用制限 | HostのClaude Codeのアカウントが使える量の上限。5時間制限と週間制限がある | 利用枠、クォータ、使用量 | usage limit |
| 5時間制限 | 5時間ごとにリセットされる利用制限 | 5h枠、5時間枠 | 5-hour limit |
| 週間制限 | 週ごとにリセットされる利用制限 | weekly枠、週枠 | weekly limit |
| ツール動作確認 | Agent起動の前に、リポジトリが必要とするツールがHostで動くかを、コマンドで確かめること | 道具の確認、tool check | tool check |
| 使用率 | 5時間制限か週間制限のうち、使った割合 | 利用率、消費率 | usage |
| ペースの上限 | 週間制限の上限。週の経過とともに上がる | | pace limit |
| 手元の状態 | cuminがHostに持つ、失っても作業を失わない情報。セッションの番号、回数、使用率など | ローカルDB、キャッシュ | local state |
| モニターファイル | cuminが定期確認のたびに書き、cuminを外から見る道具が読むファイル。cumin自身は読まない | 監視ファイル、ステータスファイル | monitor file |
| 専門知識 | 1つの分野の作業の基準。roleの契約とは別に、Agentに渡す。`disciplines/`に置く | discipline、規律、専門スキル | discipline |

## 要件文書とテスト

| 語 | 意味 | 使わない言い方 | 英語 |
|---|---|---|---|
| 要件 | IDを持つ、真偽を確かめられる1つのルール。書式はcumin-traceの要件文書の書式に従う | | requirement |
| カテゴリ | 要件を分ける単位。1つのカテゴリが、1つのディレクトリと1つのページになる | 領域、エリア | area |
| 検証 | 1つの要件が満たされたことの確かめ方 | 確認方法 | verification |
| 受け入れテスト | いくつかの要件をまとめて、利用者の場面で確かめるテスト。自動テストのほか、実機で動かして確かめるものも含む | 上位要件のテスト、受け入れのシナリオ | acceptance test |
