# 用語集

cumin-works の文書で使う語の意味を決める。「使わない言い方」にある言い方は、文書では使わない。「英語」は、GitHub に残す文章やコードで使う訳である。

## 人と役目

| 語 | 意味 | 使わない言い方 | 英語 |
|---|---|---|---|
| Maintainer | 対象のリポジトリに write 以上の権限を持つ、人のアカウント。要求Issueを書き、進めてよいと合図し、merge を判断する | メンテナ、Owner | Maintainer |
| Issue Owner | その Issue に今付いている `cumin/status/ready` を付けた Maintainer | Owner、担当者 | Issue Owner |
| Operator | Host で cumin を動かす人。利用枠はこの人のものである | 運用者、管理者 | Operator |
| Agent | cumin が起動して作業させる AI。今は headless の Claude Code である | エージェント、AI、bot | agent |
| role | Agent が受け持つ役目。Planner、Implementer、Reviewer の3つがある | ロール、Agent Role | role |
| Planner | 要求Issueを分割し、受け入れの確認をする role | | Planner |
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
| 既定のブランチ | 対象のリポジトリの default branch | main ブランチ、デフォルトブランチ | default branch |
| 作業場所 | Agent が作業する、Issue ごとの git worktree | ワークツリー | work directory |
| セッション | Agent の会話の続き。続きの依頼は、同じセッションで行うことがある | 会話、スレッド | session |

## Issue と状態

| 語 | 意味 | 使わない言い方 | 英語 |
|---|---|---|---|
| 要求 | Maintainer が実現したいこと。要求Issueに書く | | need |
| 要求Issue | 要求を書いた Issue。`cumin/type/requirement` が付く | 要件Issue、親Issue | requirement issue |
| 実装Issue | 要求Issueの sub-issue で、1つの Pull Request で実装する単位 | 子Issue、タスク | implementation issue |
| sub-issue | GitHub の親子関係で、ある Issue の子になっている Issue | サブIssue、子Issue | sub-issue |
| 状態 | Issue がワークフローのどこにいるか。状態ラベル1つで表す | ステータス | state |
| 状態ラベル | 状態を表す `cumin/status/*` のラベル | ステータスラベル | status label |
| 下書き | 状態ラベルのない Issue。cumin は扱わない | | draft |
| 遷移 | 状態が移ること。同じ状態にとどまる遷移もある | | transition |
| 動作 | 遷移で cumin が行うこと。英語の短い名前を持つ (例: request the split) | アクション、処理 | action |
| 定期確認 | cumin が一定の間隔で GitHub を読み、動作を決めること | ポーリング、巡回 | poll |
| 空き | 同時に動かせる Agent の数に、余裕があること | 枠 | free slot |
| 依頼 | cumin が Agent を起動して、作業を頼むこと | 指示、発注 | request |
| 起動 | Agent のプロセスを始めること | 実行開始 | start |
| 通知 | Host の設定で決めた通知先に知らせること。文書には相手の名前を書かない | 連絡、アラート | notify |

## 作業の流れ

| 語 | 意味 | 使わない言い方 | 英語 |
|---|---|---|---|
| 分割 | Planner が要求Issueを実装Issueに分けること | 分解、計画 | split |
| 分割結果の確認 | Maintainer が、分割の結果と sub-issue を確かめること | plan review、プランレビュー | plan review |
| risk | 実装Issueの merge の risk。`risk/*` のラベルで表す | リスク | risk |
| 保護されたパス | Agent が変えてはいけないファイルのパス | 保護パス | protected path |
| 必須の check | 既定のブランチの保護で、merge の前に通る必要がある check | 必須チェック | required check |
| ラウンド | Reviewer のレビュー1回と、それへの修正の組 | 周回 | round |
| merge の判断 | Maintainer が Pull Request を見て、merge するかを決めること | マージ判定 | merge decision |
| 受け入れの確認 | Planner が、merge された結果が要求Issueを満たすかを確かめること | 受け入れ確認、受入確認 | acceptance check |
| 受け入れ | Maintainer が受け入れの確認を見て、要求Issueを閉じること | 承認、完了確認 | acceptance |
| 差し戻し | Maintainer が受け入れずに、sub-issue を足して進め直すこと | 却下、やり直し | send back |
| フォローアップノート | merge のあとに残った指摘を、cumin が要求Issueにまとめて書くコメント | Follow-up コメント | follow-up note |

## 利用枠と道具

| 語 | 意味 | 使わない言い方 | 英語 |
|---|---|---|---|
| 利用枠 | Host の Claude Code のアカウントが使える量の上限。5h枠と weekly枠がある | クォータ、quota、使用量 | quota |
| 5h枠 | 5時間ごとにリセットされる利用枠 | 5時間枠 | 5-hour window |
| weekly枠 | 週ごとにリセットされる利用枠 | 週枠 | weekly window |
| 道具の確認 | Agent を起動する前に、リポジトリが必要とする道具が Host で動くかを確かめるコマンド | tool check | tool check |
| discipline | 1つの分野の作業の基準。role の契約とは別に、Agent に渡す | 規律 | discipline |

## 要件文書

| 語 | 意味 | 使わない言い方 | 英語 |
|---|---|---|---|
| 要件 | ID を持つ、真偽を確かめられる1つのルール。書式は cumin-trace の要件文書の書式に従う | | requirement |
| カテゴリ | 要件を分ける単位。1つのカテゴリが、1つのディレクトリと1つのページになる | 領域、エリア | area |
| 検証 | 要件が満たされたことの確かめ方 | 上位要件のテスト、確認方法 | verification |
| 受け入れのシナリオ | いくつかの要件をまとめて確かめる一続きの場面。ID を持たない | | acceptance scenario |
