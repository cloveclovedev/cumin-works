# cumin-works 要求仕様書

cumin-works は、GitHub の Issue と Pull Request を見ながら、決められたルールだけで Agent を起動し、計画、実装、レビュー、マージまでを進めるワークフローのエンジンである。人は要求を書き、要所で判断する。

## 解決したい課題

1. Agent を使える利用制限があっても、人が手を動かしていない時間は、ほとんど使われずに残る。
2. Agent を使い始めるきっかけが、人のやる気と空き時間になっている。人が忙しいほど、Agent も動かない。
3. 時刻で起動するような自動化はできても、それだけでは、マージされる成果物に結びつく使い方にならない。何を頼み、結果をどう確かめ、次に何をするかを、毎回人が決めている。

## 目的

Maintainer の負担をできるだけ減らしながら、Agent を動かし続け、時間あたりの成果を最大にする。

## 指標

どれも、GitHub と cumin が読む利用制限の使用率から、週ごとに数えられる。

- 稼働: 週の終わりの週間制限の使用率。設定の目標に近いほどよい。稼働は成果そのものではない。Agent が動き続けることを直近の目標にするための、暫定の指標である。
- 成果: 週ごとの、マージされた実装Issueの数と、受け入れられた要求Issueの数。
- Maintainer の負担: 要求Issue 1つあたりで、その要求Issueと sub-issue が Maintainer の番の状態 (`awaiting-` で始まる状態) になった回数。少ないほどよい。

## 登場人物

人と Agent:

- Maintainer: 対象のリポジトリに write 以上の権限を持つ人。要求Issueを書き、進めてよいと合図する。
- Assignee: その Issue の指示と判断を受け持つ Maintainer。GitHub の Issue の assignee で表し、1つの Issue に1人だけ付ける。
- Operator: Host で cumin を動かし、Host の設定と利用制限を受け持つ。
- Agent: cumin が起動する AI。Planner、Implementer、Reviewer の3つの role がある。
  - Planner: 要求Issueを計画し、マージされた結果を受け入れ確認する。
  - Implementer: 実装Issueを実装し、Pull Request にまとめる。
  - Reviewer: Pull Request をレビューする。

Maintainer と Assignee がすること:

| すること | 誰がするか |
|---|---|
| 要求Issueを書き、`cumin/type/requirement` を付ける | Maintainer |
| `cumin/status/ready` を付けて、進めてよいと合図する。Assignee がいなければ、cumin が付けた人を Assignee にする | Maintainer |
| 要求Issueどうしに blocked by を張り、優先度のラベルを付ける | Maintainer |
| Agent への指示を、Issue のコメントに書く | その Issue の Assignee |
| 計画結果の確認 (sub-issue に `cumin/status/ready` を付ける、要らない sub-issue を閉じる) | 要求Issueの Assignee |
| マージ判断 (Pull Request の承認と、変更の依頼) | 実装Issueの Assignee |
| 受け入れ (要求Issueを閉じる) と差し戻し | 要求Issueの Assignee |
| `cumin/status/awaiting-decision` の Issue に答える (答えを書き、`cumin/status/ready` を付け直す) | その Issue の Assignee |
| `cumin/type/owner-task` の作業をして、その Issue を閉じる | その Issue の Assignee |
| 状態ラベルを手で直す | Maintainer |

担当を替えるときは、Maintainer が Assignee を替える。Agent は動き直さない。

システム:

- cumin: Host で常駐し、GitHub を定期確認して、状態ラベルを替え、Agent起動とマージを行う。
- Host: cumin が動くマシン。macOS を対象にする。
- GitHub: 要求、作業、成果物と、その状態の正本。cumin と Agent は、role ごとの GitHub App で GitHub を使う。GitHub App の名前は、運用する組織ごとに決める。

## 原則

ワークフローの原則:

- Agent の判断で要求から外れないように、適切なところで Maintainer に判断を求める。
- Maintainer が判断するときの負担を、できるだけ小さくする。
  - 変更を、1回で判断できる大きさに分ける。
  - 判断に要る情報 (変更の内容と、全体の中での位置) を、できるだけ図で示す。
- 人が見ていない間も Agent を動かせるように、Agent には、その作業に要る最小の権限だけを渡す。人の認証情報は渡さない。

cumin の原則:

- cumin は、決められたルールだけで動く。AI による判断を含まない。要求の解釈、計画、実装、レビュー、リスクの判断は、すべて Agent か Maintainer が行う。
- 同じ GitHub の状態からは、いつも同じアクションを決める。
- 状態は GitHub に置く。cumin が手元に持つのは、失っても作業を失わないものだけである。
- 完了は、GitHub の上の事実で決める。Agent の申告では決めない。

## 範囲外

- Windows では動かさない。Linux への対応は [バックログ](backlog.md) にある。

## 進め方

- シンプルファースト: 要件を満たす、いちばん単純な方法を探す。AI の道具は速く変わる。
- 要件ファースト: すべての振る舞いは、要件にたどれる。Maintainer が要件の全体を把握できる大きさに保つ。
- ドキュメントファースト: 文書は、動いているところまで、いつも追いついている。
- テストファースト: 細かいユニットテストを多く書くより、受け入れテストで要件を確かめることを優先する。

## フェーズ

バージョンごとの目標を書く。期日の目安と、どの要求Issueがどのバージョンに入るかは、GitHub のマイルストーンで管理する。

| バージョン | 目標 |
|---|---|
| v0.1 | peppercheck の作り直しを、cumin-works の上で進められる |
| v0.2 | 実際に使って分かったことを直し、コードと文書を整理する。新しい機能は足さない |
| v0.3 | 個人のアカウントでセットアップでき、Free プランの private リポジトリでも、限られたワークフローで使える |
| v1.0 | cumin-works を公開する。peppercheck の作り直しと cumin-works 自身の開発が、cumin の上で問題なく進む |

要件のページにあるのは、今のフェーズまでに作ると決めたことである。あとで取り入れたいことは [バックログ](backlog.md) にある。
