# Chief Engineerの要件

どのroleにも当てはまる要件は [Agentに共通の要件](common.md) にある。この文書には、Chief Engineerに固有のことだけを書く。

## 役割

要求Issueを1つ受け取り、実装Issueに分割する。実装Issueどうしの依存関係を記録し、それぞれにriskを仮に付ける。sub-issueが全て閉じたら、まとめた結果が要求を満たしているかを確かめる (受け入れの確認)。

Chief Engineerは分割に責任を持つ。コードは書かない。分割結果を承認するのはOwnerである。

## いつ起動されるか

cuminが次のときに起動する。番号は [Issueのラベルと状態遷移](../workflow/issue-states.md) の表に対応する。

| 依頼の種類 | きっかけ | セッション |
|---|---|---|
| 分割 | R1: Ownerが、`cumin/type/requirement` の付いた要求Issueに `cumin/status/ready` を付けた | 新しいセッション |
| 受け入れの確認 | R4: 要求Issueのsub-issueが全て閉じた | 新しいセッション |

## 入力

cuminが依頼のたびに渡すもの:

- 対象のリポジトリと、要求Issueの番号
- 作業場所。cuminがmainの最新の内容で作業ディレクトリを用意する。Chief Engineerは読むだけで、変更しない
- roleとしての指示。[実装Issueの分割基準](../policies/issue-sizing.md)、[要求Issueの分割基準](../policies/requirement-sizing.md) の上限、riskの基準を含む

Chief Engineerが自分で読むもの:

- 要求Issueと、そこからリンクされた文書
- リポジトリの中身。リポジトリにある指示を含む
- 要求Issueに既に付いているsub-issue (やり直しに備える。下の「出力」を参照)

## 出力

GitHubに残すもの:

- 実装Issue。要求Issueのsub-issueとして作る。1つの実装Issueが、1つのPull Requestになる
- 実装Issueどうしの依存関係 (blocked by)。同じ要求Issueのsub-issueの間だけに張る。他の要求Issueへの依存は、Ownerが要求Issueどうしに張る
- それぞれの実装Issueに、`risk/*` のラベルをちょうど1つ
- 要求Issueにmilestoneが付いていれば、それぞれの実装Issueに同じmilestoneを付ける
- 要求Issueへのコメントを1つ。分割の全体像を、Ownerが確認しやすい形で書く。形式は [plan-summary.md](../../../../templates/plan-summary.md) に従う

実装Issueの本文は、[implementation-issue.md](../../../../templates/implementation-issue.md) の6つの節で書く。

| 節 | 内容 |
|---|---|
| Context | なぜこの作業が要るか。要求Issueのどの部分に当たるか |
| Scope | やること。やらないことも書く |
| Pointers | 変更するファイルやパッケージ。手本にする既存のコード |
| Acceptance criteria | 正しいか正しくないかを確かめられる形で書く。テストと文書に期待することも書く |
| How to verify | 確かめるためのコマンドと、期待する結果 |
| Related documents | 読むべき要件文書、設計文書、インターフェース |

やり直しに備えること:

- 実装Issueを作る前に、要求Issueに既に付いているsub-issueを確かめ、同じものを二重に作らない。異常終了のあとにcuminが同じ依頼をやり直しても、結果が変わらないようにする

受け入れの確認を依頼されたときに、GitHubに残すもの:

- 要求Issueへのコメントを1つ。形式は [acceptance-check.md](../../../../templates/acceptance-check.md) に従う
- mainの最新の内容で、要求Issueの Requirements と Constraints を1項目ずつ確かめる。項目ごとに、結果 (Pass か Fail)、証拠 (実行したコマンドと結果、または読んだファイル)、対応したPull Requestを書く
- 残っている作業を一覧にする。要求Issueに付いたフォローアップノートから、重複と、あとのPull Requestで済んだものを除く。Pull Requestの説明の `Follow-up` 以外の場所に書かれた、範囲の外の作業も拾う
- Failがあっても、直さない。Issueを作らず、変更もしない。代わりに、Failごとに、どう直すかの提案をコメントに書く。足すとよいsub-issueの題と、やることを1〜2文で書く。どうするかはOwnerが決める

実行の最後にcuminに返すもの:

- [共通の形式](common.md#結果の返し方) のJSON。Chief Engineerに固有の項目はない

## してよいこと、してはいけないこと

GitHub上では `cumin-chief-engineer` として振る舞う。持っている権限は、Issueの読み書きと、コードの読み取りである。

してはいけないこと:

- コードを変更しない。ブランチやPull Requestを作らない (権限でも止める)
- 要求Issueの本文を書き換えない。伝えたいことはコメントに書く
- `cumin/status/*` のラベルを付けない。実装Issueに `cumin/status/ready` を付けるのはOwnerである
- 要求Issueに書かれていないことを、実装Issueに足さない。必要だと考えたことは、要求Issueへのコメントで提案する

## riskの基準

| risk | 基準 |
|---|---|
| `risk/low` | 数行で、影響が自明な変更 |
| `risk/high` | revertで戻せない変更。DBマイグレーション、デプロイやCIの設定、認証や決済、外部サービスへの副作用、公開APIの契約、cumin自身のルール (`.cumin/`) |
| `risk/medium` | それ以外の全て |

この表は初期値である。Hostかリポジトリに `risk-criteria.md` があれば、cuminはその内容を、表の代わりに指示に入れる (置き場所と優先順位は [cumin本体の要件](../cumin-core.md) の「設定」にある)。Reviewerにも、同じ基準が渡る。

迷ったら高いほうを付ける。riskを確定するのはOwnerである。

## 完了の条件

Chief Engineerが `done` を返す前に自分で確かめること:

- 全ての実装Issueが、分割基準の1〜9を満たしている
- 実装Issueを合わせると、要求Issueの範囲を全て覆っている
- 依存関係が循環していない
- `risk/high` に当たる変更が、それだけの最小の実装Issueに隔離されている

cuminが `done` を受けて、GitHub上で確かめること:

- 要求Issueにsub-issueが1つ以上ある
- 全てのsub-issueに、`risk/*` のラベルがちょうど1つ付いている

確かめた結果が合っていれば、cuminは要求Issueを `cumin/status/awaiting-owner-review` に替えて、Ownerに知らせる。

受け入れの確認では、cuminは、最後のsub-issueが閉じたあとに書かれた `## Acceptance check` のコメントが、要求Issueにあることを確かめる。表の結果は読まない。

## blocked を返すとき

次のどれかに当たったら、推測で分割せずに `blocked` を返す。

- 要求が曖昧で、完了条件を書けない
- 要求の中に食い違いがある
- 意味のある設計案が複数あり、どれを選ぶかで分割が変わる。このときは、選択肢とそれぞれの得失を `blocked_reason` に書く
- 要求が大きすぎる。実装Issueの見込みが、[要求Issueの分割基準](../policies/requirement-sizing.md) の上限 (12個) を超える。このときは、要求Issueの分け方の案を `blocked_reason` に書く

## 上位要件のテスト

| # | 場面 | 期待する結果 |
|---|---|---|
| 1 | 内容のはっきりした要求Issueを渡す | 実装Issueがsub-issueとして作られ、それぞれに6つの節と `risk/*` が1つある。依存関係が記録され、要求Issueに分割の全体像のコメントが付く。結果は `done` |
| 2 | DBマイグレーションを含む要求Issueを渡す | マイグレーションだけの実装Issueが分けて作られ、`risk/high` が付く |
| 3 | 完了条件を書けないほど曖昧な要求Issueを渡す | 実装Issueは作られない。結果は `blocked` で、何が決まっていないかが理由に書いてある |
| 4 | 実装Issueを途中まで作ったところで止め、同じ依頼をやり直す | 同じ実装Issueが二重に作られない |
| 5 | コードの変更と、Pull Requestの作成を試みる | どちらも権限で拒否される |
| 6 | 実装Issueが12個を超える見込みの、大きな要求Issueを渡す | 実装Issueは作られない。結果は `blocked` で、要求Issueの分け方の案が理由に書いてある |
| 7 | sub-issueが全て閉じた要求Issueで、受け入れの確認を依頼する | 要求Issueに `## Acceptance check` のコメントが1つ付く。Requirements の項目ごとに、結果と証拠がある。Issueは作られず、変更もされない。結果は `done` |
| 8 | Requirements の1つが満たされていない状態で、受け入れの確認を依頼する | その項目が Fail になり、何が足りないかが証拠と一緒に書いてある。どう直すかの提案が書いてある。Issueは作られない |
