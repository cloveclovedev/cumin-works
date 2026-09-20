# Implementerの要件

どのroleにも当てはまる要件は [Agentに共通の要件](common.md) にある。この文書には、Implementerに固有のことだけを書く。

## 役割

実装Issueを1つ受け取り、その内容を実装して、1つのPull Requestにまとめる。必須のcheckの失敗と、Reviewerの指摘も直す。

Implementerは実装に責任を持つ。Issueの分割、レビュー、merge、ラベルの付け替えには関わらない。

## いつ起動されるか

cuminが次のときに起動する。番号は [Issueのラベルと状態遷移](../workflow/issue-states.md) の表に対応する。

| 依頼の種類 | きっかけ | セッション |
|---|---|---|
| 実装 | I1: `cumin/status/ready` の付いた実装Issueに着手する。Pull Requestはまだない | 新しいセッション |
| 続き | I1: Ownerが差し戻したか回答したあとに、`cumin/status/ready` が付け直された。Pull Requestは既にある | 新しいセッション |
| checkの修正 | I4: 必須のcheckが失敗した | 直前と同じセッション |
| 指摘の修正 | I5: Reviewerが `REQUEST_CHANGES` を出した | 直前と同じセッション |
| 衝突の解消 | I6: mergeしようとしたら衝突した | 直前と同じセッション |

## 入力

cuminが依頼のたびに渡すもの:

- 対象のリポジトリと、実装Issueの番号
- 依頼の種類 (上の表のどれか)
- 作業場所。cuminが `git worktree` でIssueごとに独立した作業ディレクトリを用意し、その中で起動する。並行して進む他のIssueの作業と混ざらない。作業ディレクトリを置く場所は、cuminの設定ファイルで変えられる
- ブランチの名前。cuminが決めて渡す (`cumin/<Issueの番号>-<短い説明>` の形)
- 依頼の種類に応じた材料。失敗したcheckの名前と内容、または対象のレビュー
- roleとしての指示

Implementerが自分で読むもの:

- 実装Issueと、その上の要求Issue。Issueからリンクされた文書
- リポジトリの中身。リポジトリにある指示 (`CLAUDE.md`、`AGENTS.md`、skillなど) を含む
- 続きの依頼では、Pull Request、レビュー、Ownerのコメント

## 出力

GitHubに残すもの:

- 渡された名前のブランチと、その上のコミット
- 1つの実装Issueにつき、1つのPull Request。説明は [pull-request.md](../../../../templates/pull-request.md) の形式で書く。本文に `Closes #<実装Issueの番号>` を書き、mergeされたら実装Issueが閉じるようにする
- 指摘の修正を依頼されたときは、`(blocking)` の指摘の全てに返答する。形式は [review-reply.md](../../../../templates/review-reply.md) に従う
- `(non-blocking)` の指摘は、数行で直せて、実装Issueの範囲の内にあるものだけを、同じラウンドで直してよい。直したら `Fixed` と返答する。それ以外は、直さずに残す
- 自分で気づいた範囲の外の作業は、Pull Requestの説明の `Follow-up` に書く。Issueは作らない。`(non-blocking)` の指摘を `Follow-up` に写さない

残った `Follow-up` と、対応されなかった `(non-blocking)` の指摘は、mergeのあとに、cuminがフォローアップノートとして要求Issueに転記する。Implementerは何もしなくてよい。
- 続きや修正の依頼では、新しいPull Requestを作らずに、同じPull Requestにコミットを積む

実行の最後にcuminに返すもの:

- [共通の形式](common.md#結果の返し方) のJSON。Implementerに固有の項目はない

## してよいこと、してはいけないこと

GitHub上では `cumin-implementer` として振る舞う。持っている権限は、コードの読み書き、Pull Requestの読み書き、Issueの読み取りである。

してはいけないこと:

- mainに直接pushしない。Pull Requestをmergeしない (rulesetでも止める)
- Issueの本文を書き換えない
- 保護されたパスを変更しない。保護されたパスはリポジトリの `.cumin/` で指定する。変更が必要だと分かったら、変更せずに `blocked` を返す
- `.github/workflows` を変更しない (権限でも止める)
- 実装Issueに書かれていない範囲に手を広げない。必要だと分かったら、Pull Requestの本文に書いて知らせる
- レビューを待たない。checkの完了を待たない。どちらもcuminが受け持つ

## 完了の条件

Implementerが `done` を返す前に自分で確かめること:

- 実装Issueの完了条件を満たしている
- 手元で実行できるテスト、ビルド、lintが通っている
- 変更をpushしてあり、Pull Requestが開いている

cuminが `done` を受けて、GitHub上で確かめること:

- このIssueを閉じるPull Requestが開いている
- ブランチの先頭のコミットがpushされている
- 必須のcheckが全て通っている。保護されたパスが変更されていないことも、必須のcheckの1つとして確かめる (下の「保護されたパスのcheck」)

cuminは、Implementerの `done` という申告ではなく、この事実で完了を判定する。

## 保護されたパスのcheck

保護されたパスが変更されていないことは、cuminではなくGitHubの機能で確かめる。

- 対象のリポジトリに、GitHub Actionsのworkflowを置く。workflowは、Pull Requestの変更ファイルの一覧を、保護されたパスの一覧と突き合わせ、1つでも当たれば失敗する。
- このcheckを、mainのrulesetで必須のcheckに登録する。
- checkのjobには、「Pull Requestの作成者がBot (GitHub Appなど) のときだけ実行する」という条件を付ける。作成者が人のときはjobが飛ばされる。GitHubは、条件で飛ばされたjobを成功として扱うので、OwnerのPull Requestはこのcheckで止まらない。
- 条件に、GitHub Appの名前は使わない。名前で「checkを掛ける相手」を指定すると、名前を間違えたときに、jobが飛ばされて成功の扱いになり、保護が黙って無効になるためである。Botの全てに掛ければ、間違える名前がない。
- 保護されたパスの一覧は、Pull Requestのブランチではなくmainにあるものを読む。一覧そのもの (`.cumin/`) も保護されたパスに含める。こうしないと、Implementerが同じPull Requestの中で一覧を書き換えて、checkをすり抜けられる。
- Implementerは `.github/workflows` を変更する権限を持たないので、workflowそのものを書き換えることもできない。

checkが失敗したときは、他の必須のcheckと同じくI4に乗り、cuminがImplementerに修正を依頼する。cuminに専用の仕組みは要らない。

## blocked を返すとき

次のどれかに当たったら、推測で進めずに `blocked` を返す。cuminはやり直さずに、1回目から `cumin/status/awaiting-owner-decision` に替えてOwnerに知らせる。

- 実装に必要な要件が足りない、または要件どうしが食い違っている
- 保護されたパスや `.github/workflows` の変更が必要である
- 実装Issueが、1つのPull Requestに収まらない大きさだと分かった
- 先に終わっているはずの作業 (blocked by のIssue) が、実際には足りていない

## 上位要件のテスト

この要件が満たされていることを、次の場面で確かめる。

| # | 場面 | 期待する結果 |
|---|---|---|
| 1 | `cumin/status/ready` の実装Issueを渡す | 渡した名前のブランチにPull Requestが1つ開き、本文に `Closes #N` がある。結果は `done` |
| 2 | 保護されたパスの変更が必要な実装Issueを渡す | 保護されたパスは変更されない。結果は `blocked` で、理由にそのパスが書いてある |
| 3 | 要件の足りない実装Issueを渡す | 結果は `blocked` で、何が決まっていないかが理由に書いてある |
| 4 | 必須のcheckを失敗させて、修正を依頼する | 同じPull Requestに修正が積まれ、checkが通る |
| 5 | Ownerが差し戻したあとに、続きを依頼する | 新しいセッションで起動される。新しいPull Requestは作られず、同じPull Requestに修正が積まれる |
| 6 | mainへのpushと、Pull Requestのmergeを試みる | どちらもrulesetに拒否される |
