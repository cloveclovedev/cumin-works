# Agentに共通の要件

Planner、Implementer、Reviewerのどのroleにも当てはまる要件。roleごとの文書は、ここに書いたことを繰り返さない。

## 結果の返し方

Agentは実行の最後に、次の項目を持つJSONを1つ返す。

| 項目 | 型 | 内容 |
|---|---|---|
| `result` | 文字列 | `done` または `blocked` |
| `summary` | 文字列 | 何をしたかを1〜3文で書く |
| `blocked_reason` | 文字列 | `blocked` のときは、Ownerに判断を求める文章を、[decision-request.md](../../../../templates/decision-request.md) の形式のMarkdownで書く。`done` のときは空文字列にする |

例:

```json
{
  "result": "blocked",
  "summary": "I started the login screen. I stopped because the sign-in method is not decided.",
  "blocked_reason": "## Decision needed: Which sign-in method does the login screen use?\n\nType: Blocked\n..."
}
```

JSON Schema:

```json
{
  "type": "object",
  "properties": {
    "result": { "type": "string", "enum": ["done", "blocked"] },
    "summary": { "type": "string" },
    "blocked_reason": { "type": "string" }
  },
  "required": ["result", "summary", "blocked_reason"],
  "additionalProperties": false
}
```

この形式についての決まり:

- 項目と意味は、cuminとAgentの間の約束であり、Agentを動かすCLIが何であっても変わらない。
- CLIにこの形式を守らせる方法は、CLIごとに違う。Claude Codeは `--json-schema`、Codexは `--output-schema` でJSON Schemaを渡せる (どちらも公式ドキュメントで確認済み)。この違いはcuminの中の、CLIごとの接続部分に閉じ込める。
- cuminは、返ってきたJSONを自分でも検証する。形式に合わない、または `blocked` なのに `blocked_reason` が空なら、異常終了として扱う。
- `done` は「確かめ始めてよい」という合図でしかない。完了したかどうかは、cuminがGitHub上の事実で判定する。何を確かめるかは、roleごとの文書に書く。

## 指示の渡し方

- roleとしての指示は、cuminが依頼のたびに渡す。
- Agentは、Hostのユーザアカウントに置かれたグローバルな指示や設定 (`~/.claude` など) を読まない。読むと、何を根拠に動いたのかが追えなくなる。
- 対象のリポジトリにある指示 (`CLAUDE.md`、`AGENTS.md`、skillなど) は、Agentが読んでよい。リポジトリに固有の知識は、リポジトリに置く。

グローバルな指示を読ませない方法:

- cuminは、Agentを起動するときのオプションで、ユーザアカウントの設定の読み込みを止める。Claude Codeでは `--setting-sources project` を付ける。これでユーザアカウントの `CLAUDE.md` が読み込まれなくなり、サブスクリプションのログインはそのまま使えることを、実機で確かめた。
- Claude Codeの自動メモリ (Agentが自分で書き残し、次のセッションで読み込まれるメモ) は、このオプションでは止まらない。cuminは、自動メモリも切って起動する。次のAgentに引き継ぐべきことは、GitHubに書く。
- 同じことができないCLIを使うときは、cumin user accountにグローバルな指示や設定を置かない、という運用で守る。cuminは起動時に、グローバルな指示のファイルがあるかを確かめ、あれば警告する。

## 指示の合成

roleとしての指示は、4つの部分をこの順につないだ1つの文章である。

| 順 | 部分 | 置き場所 | 内容 |
|---|---|---|---|
| 1 | roleのファイル | `roles/<role>.md` | cuminとAgentの約束。roleの身元と境目、読むもの、作業場所とブランチ、GitHubに残すもの、範囲と保護されたパス、返す結果、cuminの事情による `blocked` の条件、テンプレートに従う義務 |
| 2 | disciplineのファイル | `disciplines/<discipline>/<role>.md` | 分野の基準。仕事が終わったことの確かめ方、コミットメッセージの決まり、そのroleにとっての良い仕事、分野の事情による `blocked` の条件 |
| 3 | 平易な英語の決まり | `templates/writing-rules.md` | Agentが書く全ての文章に効くので、指示の本文に入れる |
| 4 | riskの基準 | disciplineの `risk-criteria.md`、またはHostかリポジトリのファイル | 対象のリポジトリごとに決まる文章。3段の優先順位は [cumin本体の要件](../cumin-core.md) の「設定」にある |

- 合成するのはcuminの `internal/agent` である。文章を持つ3つの置き場所は、どれも自分のMarkdownを読むだけで、互いを知らない。
- riskの基準は、対象のリポジトリごとに決まるので、起動の依頼のデータとして渡す。tokenや作業場所と同じ扱いである。riskの基準がない起動 (使用率を読む最小の実行など) では、1から3だけを受け取る。
- disciplineとは、roleが扱う分野のことである。roleはcuminが動かす箱で、disciplineはその箱を満たす分野である。
- disciplineはroleに足すだけで、roleの決まりを緩めない。食い違ったらroleが勝つ。この決まりは、roleのファイルに書く。
- disciplineのファイルがないroleは、その部分を飛ばす。エラーにはしない。
- v0.1が持つdisciplineは、ソフトウェア開発の1つだけである。どのroleも同じdisciplineで動き、選ぶ仕組みはない。分野を選ぶ仕組みは [backlog](../backlog.md) の「roleとdisciplineの分離」にある。
- 1つの行動のためのテンプレートは、指示ではなくskillとして渡す ([GitHubに残す文章のテンプレート](../policies/writing-templates.md) の「置き場所」)。

## GitHubに残す文章

- Issue、Pull Request、レビュー、コメント、コミットメッセージは、英語で書く。
- 平易な英語で書く。短い文にする。慣用句や凝った言い回しを使わない。同じものは、いつも同じ言葉で呼ぶ。
- 文章の型 (実装Issue、Pull Requestの説明、レビューの指摘、指摘への返答、Ownerへの報告) は、テンプレートに従う。テンプレートと、その狙いは [GitHubに残す文章のテンプレート](../policies/writing-templates.md) にある。
- `blocked_reason` と `summary` も英語で書く。cuminが `blocked_reason` をIssueのコメントとして投稿するためである。

## GitHub上の身元

- Agentは、自分のroleのGitHub Appとして振る舞う。
- cuminは依頼のたびに、そのroleのAppのtokenだけをAgentに渡す。tokenは1時間で失効する。
- Agentは、Ownerの認証情報を使わない。

## 起動の依頼の事実

cuminは、起動の依頼の先頭に、その実行の事実をラベル付きの行の並びで書く。cuminがすでに知っている事実なので、Agentは推測で求めない。

| 事実 | 中身 | 出どころ |
|---|---|---|
| 扱うIssue | この実行が扱うIssueの番号と種類 (要求Issueか実装Issueか)。Agentが止まるときは、このIssueを「止めたIssue」として、decision requestを書く | cuminの依頼 |
| Ownerのログイン名 | このIssueに最新の `cumin/status/ready` を付けたアカウントが、Owner ([cumin本体の要件](../cumin-core.md) の「Owner」) であるときの、その名前。要求Issueにそのイベントがなければ、sub-issueの最新のものを使う。どれもないとき、またはそのアカウントがOwnerでないときは、「Ownerのログイン名はない」と書く。Agentは、このアカウントのコメントを、Ownerの指示として読む | GitHubのラベルのイベントと権限 |
| 保護されたパス | 対象のリポジトリで効いている一覧と、その照合の決まり。cuminが既定のブランチの `.cumin/config.toml` から読む。一覧がなければ初期値 ([cumin本体の要件](../cumin-core.md) の「設定」) | リポジトリの設定 |

時間の上限と終わる時刻も、同じ並びに書く (下の「プロセスとセッション」)。Plannerの起動の依頼には、ImplementerとReviewerの時間の上限 (`roles.<role>.time_limit`) も書く。Plannerが、1つの実装Issueがそれぞれの1回の実行に収まるように分けるためである ([実装Issueの分割基準](../policies/issue-sizing.md) の10)。

## プロセスとセッション

- プロセスは依頼のたびに起動し、依頼が終わったら終了する。何かを待つ間、Agentは動いておらず、利用枠を使わない。
- Ownerの介入を挟まない一続きの作業は、同じセッションで続ける。
- Ownerが介入したあと (`cumin/status/ready` の付け直し) は、セッションを新しくする。新しいセッションのAgentは、必要なことをGitHubから読み直す。
- 異常終了したら、cuminが同じ依頼を1回だけやり直す。それでも駄目なら、cuminが `cumin/status/awaiting-owner-decision` に替えてOwnerに知らせる。
- 1回の実行には時間の上限がある (`roles.<role>.time_limit`)。cuminは、その上限と、実行が終わる時刻を、起動の依頼のデータとして渡す。riskの基準と同じ扱いである。セッションを再開する実行も、その実行の終わる時刻を受け取る。
- 長い確認 (テストの繰り返しなど) は、終わる時刻よりも十分前に終わるように計画し、結果を書く時間 (Pull Requestを開く、結果を返す) を残す。この決まりは、各roleのdisciplineのファイルに書く。統計を取るための繰り返しや、待つための道具を禁じるものではない。
  - 予定の確認が終わる時刻までに終わらないときは、収まる分だけで打ち切り、何回のうち何回行ったかと、その結果を、Pull Requestの「How it was checked」に書く。足りない分は「Follow-up」に書く。
  - 受け入れ条件そのものが、実行の時間に収まらない確認を求めているときは、進めずに `blocked` を返し、理由を書く。条件を変える、Issueを分ける、`time_limit` を延ばす、のどれにするかはOwnerが決める。

## どのroleもしてはいけないこと

- `cumin/*` と `risk/*` のラベルを付け替えない。例外は、Plannerが実装Issueを作るときに `risk/*` を仮に付けることと、Ownerが手で行う作業のsub-issueに `cumin/type/owner-task` を付けることだけである。
- `docs/ja/requirements/` のような、Ownerだけが書く文書を変更しない。対象は、起動の依頼の「保護されたパス」にある。
- 推測で進めない。決まっていないことに当たったら `blocked` を返す。

## 上位要件のテスト

| # | 場面 | 期待する結果 |
|---|---|---|
| 1 | どのroleでも、cuminがAgentを起動する | 起動の依頼に、その実行の時間の上限 (`roles.<role>.time_limit`) と、実行が終わる時刻がある。disciplineのファイルに、長い確認を終わる時刻より前に終える決まりがある |
| 2 | どのroleでも、cuminがAgentを起動する | 起動の依頼に、扱うIssueの番号と種類、Ownerのログイン名 (Ownerでないときは「ない」)、保護されたパスとその照合の決まりがある |
| 3 | cuminがPlannerを起動する | 起動の依頼に、ImplementerとReviewerの時間の上限がある |
