# 用語集

cumin-works の文書で使う語の意味を決める。「使わない言い方」にある言い方は、文書では使わない。「英語」は、GitHub に残す文章やコードで使う訳である。

語の書き方は次のとおりとする。

- 人と役目の名前 (Maintainer、Planner など) と、GitHub の機能の名前 (Issue、Pull Request、sub-issue、GitHub App) は、英語のまま書く。
- それ以外の一般の語は、日本語かカタカナで書く (マージ、チェック、レビュー、リスク、ラベル)。
- 状態遷移の語は、JSTQB の Foundation Level シラバス (v4.0) の状態遷移テストの語に合わせる。テストの語も、同じシラバスのテストレベルの語に合わせる。

## 人と役目

| 語 | 意味 | 使わない言い方 | 英語 |
|---|---|---|---|
| Maintainer | 対象のリポジトリに write 以上の権限を持つ、人のアカウント。要求Issueを書き、進めてよいと合図し、マージを判断する | メンテナ、Owner | Maintainer |
| Assignee | その Issue の指示と判断を受け持つ Maintainer。GitHub の Issue の assignee で表し、1つの Issue に1人だけ付ける | Issue Owner、Owner、担当者 | assignee |
| Operator | Host で cumin を動かす人 | 運用者、管理者 | Operator |
| Agent | cumin が起動して作業させる AI。今は headless の Claude Code である | エージェント、AI、bot | agent |
| role | Agent が受け持つ役目。Planner、Implementer、Reviewer の3つがある | ロール、Agent Role | role |
| Planner | 要求Issueを計画し、受け入れ確認をする role | | Planner |
| Implementer | 実装Issueを実装し、Pull Request にまとめる role | | Implementer |
| Reviewer | Pull Request をレビューする role | | Reviewer |

## cumin と動かす場所

| 語 | 意味 | 使わない言い方 | 英語 |
|---|---|---|---|
| cumin-works | このプロダクトの名前 | cumin works v2、v2 | cumin-works |
| cumin | 動いている cumin-works。コマンドの名前でもある | 基盤、エンジン、ワークフロー基盤 | cumin |
| Host | cumin が動くマシン | ホスト、サーバ | Host |
| 対象のリポジトリ | cumin が Issue と Pull Request を扱う GitHub のリポジトリ | 製品リポジトリ | target repository |
| GitHub App | cumin と各 role が GitHub で使う身元。`cumin-core`、`cumin-planner`、`cumin-implementer`、`cumin-reviewer` | bot アカウント | GitHub App |
| main ブランチ | 対象のリポジトリの既定のブランチ。名前が main でなくても、こう呼ぶ | 既定のブランチ、デフォルトブランチ | default branch |
| Agent作業ディレクトリ | Agent が作業する、Issue ごとの git worktree。作業ディレクトリの下に置く。Agent から見えるのはこの中である | ワークスペース、作業場所 | agent work directory |
| 作業ディレクトリ | Host の上で、すべての Agent作業ディレクトリを置く親のディレクトリ。設定の `work_dir` で決める | ワークスペース、作業場所、作業ルートディレクトリ | work directory |
| セッション | Agent の会話の続き。続きを頼むときは、同じセッションで Agent を起動することがある | 会話、スレッド | session |

## Issue と状態遷移

| 語 | 意味 | 使わない言い方 | 英語 |
|---|---|---|---|
| 要求 | Maintainer が実現したいこと。要求Issueに書く | | need |
| 要求Issue | 要求を書いた Issue。`cumin/type/requirement` が付く | 要件Issue、親Issue | requirement issue |
| 実装Issue | 要求Issueの sub-issue で、1つの Pull Request で実装する単位 | 子Issue、タスク | implementation issue |
| sub-issue | GitHub の親子関係で、ある Issue の子になっている Issue | サブIssue、子Issue | sub-issue |
| ドラフト | 状態ラベルのない Issue。cumin は扱わない。GitHub の draft の Pull Request とは別のものである | 下書き | draft |
| 状態 | Issue がワークフローのどこにいるか。状態ラベル1つで表す | ステータス | state |
| 状態ラベル | 状態を表す `cumin/status/*` のラベル | ステータスラベル | status label |
| 遷移 | 状態が移ること。同じ状態にとどまる遷移もある。「イベント [ガード条件] / アクション」の形で書く | | transition |
| イベント | 遷移を始めるきっかけ。書かない遷移は、ガード条件が成り立ったときに、cumin が定期確認か Agent の実行の終わりの直後に起こす | きっかけ、トリガー | event |
| ガード条件 | 遷移が起きるために成り立っている必要がある条件 | 条件、前提 | guard condition |
| アクション | 遷移のときに cumin が行うこと。英語の短い名前を持つ (例: request the split) | 動作、処理 | action |
| 定期確認 | cumin が一定の間隔で GitHub を読み、アクションを決めること | ポーリング、巡回 | poll |
| 空き | 同時に動かせる Agent の数に、余裕があること | 枠 | free slot |
| Agent起動 | cumin が role と Issue を決めて Agent を始め、作業を頼むこと。続きを頼むときも含む | 依頼、指示、起動 | agent start |
| 通知 | Host の設定で決めた通知先に知らせること。文書には相手の名前を書かない | 連絡、アラート | notify |

## 作業の流れ

| 語 | 意味 | 使わない言い方 | 英語 |
|---|---|---|---|
| 計画 | Planner が要求Issueを読み、実装Issueに分け、依存関係とリスクを付けること | 分割、分解 | plan |
| 計画結果の確認 | Maintainer が、計画の結果と sub-issue を確かめること | 分割結果の確認、plan review、プランレビュー | plan review |
| リスク | 実装Issueのマージのリスク。`risk/*` のラベルで表す | risk | risk |
| 保護されたパス | Agent が変えてはいけないファイルのパス | 保護パス | protected path |
| 必須チェック | main ブランチの保護で、マージの前に通る必要があるチェック | 必須の check | required check |
| ラウンド | Reviewer のレビュー1回と、それへの修正の組 | 周回 | round |
| マージ判断 | Maintainer が Pull Request を見て、マージするかを決めること | merge の判断、マージ判定 | merge decision |
| 受け入れ確認 | Planner が、マージされた結果が要求Issueを満たすかを確かめること | 受け入れの確認、受入確認 | acceptance check |
| 受け入れ | Maintainer が受け入れ確認を見て、要求Issueを閉じること | 承認、完了確認 | acceptance |
| 差し戻し | Maintainer が受け入れずに、sub-issue を足して進め直すこと | 却下、やり直し | send back |
| フォローアップノート | マージのあとに残った指摘を、cumin が要求Issueにまとめて書くコメント | Follow-up コメント | follow-up note |

## 利用制限とツール

| 語 | 意味 | 使わない言い方 | 英語 |
|---|---|---|---|
| 利用制限 | Host の Claude Code のアカウントが使える量の上限。5時間制限と週間制限がある | 利用枠、クォータ、使用量 | usage limit |
| 5時間制限 | 5時間ごとにリセットされる利用制限 | 5h枠、5時間枠 | 5-hour limit |
| 週間制限 | 週ごとにリセットされる利用制限 | weekly枠、週枠 | weekly limit |
| ツール動作確認 | Agent起動の前に、リポジトリが必要とするツールが Host で動くかを、コマンドで確かめること | 道具の確認、tool check | tool check |
| 専門知識 | 1つの分野の作業の基準。role の契約とは別に、Agent に渡す。`disciplines/` に置く | discipline、規律、専門スキル | discipline |

## 要件文書とテスト

| 語 | 意味 | 使わない言い方 | 英語 |
|---|---|---|---|
| 要件 | ID を持つ、真偽を確かめられる1つのルール。書式は cumin-trace の要件文書の書式に従う | | requirement |
| カテゴリ | 要件を分ける単位。1つのカテゴリが、1つのディレクトリと1つのページになる | 領域、エリア | area |
| 検証 | 1つの要件が満たされたことの確かめ方 | 確認方法 | verification |
| 受け入れテスト | いくつかの要件をまとめて、利用者の場面で確かめるテスト。自動テストのほか、実機で動かして確かめるものも含む | 上位要件のテスト、受け入れのシナリオ | acceptance test |
