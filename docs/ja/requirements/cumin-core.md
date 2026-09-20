# cumin本体の要件

## 位置づけ

cumin本体は、Goで書くワークフローの基盤であり、Agentではない。決められたルールだけで動き、AIによる判断を含まない。

- 判断が要ることは、全てAgentかOwnerが行う。要求の解釈、分割、実装、レビュー、riskの判断がこれに当たる。
- cumin本体が行う判定は、GitHub上の事実と、Agentが返したJSONを、決まった条件に照らすことだけである。同じ状態からは、いつも同じ動作になる。

この文書では、cumin本体を単にcuminと書く。

## 受け持つこと

| 受け持つこと | 内容 |
|---|---|
| GitHubの定期確認 | 対象のリポジトリのIssue、Pull Request、check、レビューを、決まった間隔で確かめる |
| 状態の管理 | `cumin/status/*` のラベルを付け替える。条件は [Issueのラベルと状態遷移](workflow/issue-states.md) に従う。実装Issueの状態とriskのラベルを、そのIssueを閉じるPull Requestにもコピーする |
| Agentの起動 | roleごとの指示、作業場所、GitHub Appのtokenを用意して、Agentを起動する。終了を待ち、結果のJSONを検証する |
| 事実の確認 | Agentが `done` を返したあと、完了したかどうかをGitHub上の事実で確かめる |
| merge | `risk/low` で、承認され、必須のcheckが通ったPull Requestをmergeする。mergeの方法は設定で選べる (初期値はsquash) |
| フォローアップノート | Pull Requestがmergeされたら、その説明の `Follow-up` と、対応されなかった `(non-blocking)` の指摘を、フォローアップノートとして要求Issueに転記する。フォローアップノートは、要求Issueに付ける1つのコメントである。AIの判断は使わず、決まった形式から機械的に拾う |
| 通知 | Ownerの対応が要るとき、Discordのwebhookで知らせる |
| 利用枠の管理 | Agentの実行結果から使用率を読み、しきい値を超えている間は新しい着手を止める |
| Ownerからの操作の受け付け | Host上のコマンドで、状態の表示と、利用枠の使い切りの許可を受け付ける |

cuminの動作ごとのきっかけと、動く前に確かめることは、[Issueのラベルと状態遷移](workflow/issue-states.md) の表に書いてある。この文書では繰り返さない。

## 受け持たないこと

- コードを書かない。レビューしない。要求を解釈しない。riskを判断しない
- `risk/medium` と `risk/high` のPull Requestをmergeしない
- `cumin/type/requirement` と `cumin/status/ready` を付けない。この2つはOwnerの意思表示である。例外として、着手のときに `cumin/status/ready` を外す
- mainに直接pushしない。強制pushしない。mainへの変更は、Pull Requestのmergeだけで行う
- Ownerの認証情報と、リポジトリの管理者の権限 (Administration) を使わない。rulesetなどの、管理者の権限が要る準備は、管理者が自分の `gh` でスクリプトを実行して行う
- Issueを閉じない。実装Issueは、Pull RequestのmergeによってGitHubが閉じる。要求Issueは、Ownerが閉じる

## 動かし方

- Hostの上で、常駐プログラムとして動く。落ちたらlaunchdが再起動する
- Ownerが起動するのを待たない。自分からGitHubを確かめて、仕事を取りに行く
- ログは、機械で読める形で標準出力に出す
- 将来は、ハートビートを出して、外部のマシンから死活を監視できるようにする

Ownerが使うコマンド:

| コマンド | 内容 |
|---|---|
| `cumin run` | 常駐して動く。launchdから起動する |
| `cumin status` | 今の状態を表示する。実行中のAgent、Ownerの対応を待っているIssue、利用枠の使用率 |
| `cumin quota allow` | 今の5h枠を使い切ってよいと許可する。許可は、その5h枠がリセットされるまで有効。weekly枠には効かない |
| `cumin setup github-apps` | 導入のときに、Hostで実行する。roleごとのGitHub Appを登録し、秘密鍵をKeychainに入れる。もう一度実行すると、足りないroleだけを登録する |

## 利用枠の守り方

- 5h枠とweekly枠のどちらかの使用率がしきい値を超えている間、cuminは新しい着手を止める
- 使い切りを許可できるのは、5h枠だけである。Ownerの許可 (`cumin quota allow`) と、リセットが近いときにしきい値を100%にする決まりは、どちらも5h枠にだけ効く
- weekly枠のしきい値は、どの方法でも超えさせない。weekly枠を使い切ると、Ownerが何日も使えなくなるためである。これはClaude Codeでも、Codexでも同じである
- 5h枠の使い切りが許可されていても、weekly枠がしきい値を超えていれば、着手を止める

## 止めたとき、スリープしたとき

v0.1では、次のように割り切る。Hostが常時動くMac miniになれば、ほとんど起きない。

- cuminを途中で止めても、Hostがスリープしても、作業の状態はGitHubにあるので失われない
- スリープから戻ると、cuminも実行中のAgentも続きから動く。ただし、次のことが起こりうる。通信の途中だった要求が失敗する。Agentに渡したGitHub Appのtokenが、時間切れになっている (tokenは発行から1時間で失効する)。時間で区切る打ち切りが、戻った直後に働く
- どれが起きても、Agentの異常終了として扱う。同じ依頼を1回だけやり直し、それでも駄目ならOwnerに知らせる
- cuminを止めたときに作業中のラベルのまま残ったIssueは、Ownerが `cumin/status/ready` を付け直して再開する。`cumin/status/awaiting-checks` のIssueは、Agentが動いていない状態なので、cuminを起動し直せば続きから進む

## 状態の持ち方

- 作業の状態は、GitHubに置く。Issue、ラベル、Pull Request、レビュー、コメントが、状態の全てである
- cuminが手元に持つのは、失っても作業をやり直せるものだけにする。Agentのセッションの番号、checkの修正を依頼した回数、最新の使用率、使い切りの許可がこれに当たる
- レビューのラウンド数は、手元に持たずに、Pull Requestに出ている `cumin-reviewer` のレビューの数から数える
- cuminが再起動しても、GitHubを確かめ直せば、続きから動ける。ただし、作業中のラベルのまま残ったIssueを自動で回収する機能は、v0.1では作らない。Ownerが `cumin/status/ready` を付け直せば再開する

## Agentの起動

Agentの起動について、cuminが守ること。Agentの側の要件は [Agentに共通の要件](agents/common.md) にある。

- 着手のときは、Agentを起動する前にラベルを付け替える。同じIssueを二重に依頼しない
- 作業場所は、`git worktree` でIssueごとに用意する。Issueが閉じたら片付ける
- GitHub Appのtokenは、依頼のたびに、そのroleのAppの分だけを発行して渡す。Ownerの認証情報を、Agentに渡さない
- Agentが、ユーザアカウントのグローバルな指示を読まないようにして起動する。Claude Codeでは `--setting-sources project` を付け、自動メモリも切る
- Agentの環境には、そのroleのtokenだけを入れる。Hostのユーザアカウントにあるgitとghの認証の設定は、Agentに参照させない
- Agentの起動の記録に、作業場所の外にある指示やメモリ、ユーザアカウントのplugin、MCPサーバが現れたら、異常終了として扱う。Claude Codeでは、実行の最初に出る `init` のイベントで確かめられる
- Agentの実行には、時間の上限を設ける。上限を超えたら打ち切り、異常終了として扱う
- Agentは、ツールの使用を全て許可するモードで起動する。headlessの実行では、許可を尋ねられても答える人がいないためである。roleごとの制限は、GitHub Appの権限とrulesetで行う
- 結果のJSONは、cuminの側でも検証する。形式に合わなければ、異常終了として扱う
- 異常終了したら、同じ依頼を1回だけやり直す。それでも駄目なら、`cumin/status/awaiting-owner-decision` に替えてOwnerに知らせる
- Agentが `blocked` を返したら、`blocked_reason` をIssueにコメントとして投稿してから、Ownerに知らせる。Ownerは、GitHubの上で理由を読める
- roleごとに使うCLI (Claude Code、Codexなど) は、設定で選べるようにする。CLIごとの違いは、cuminの中のCLIごとの接続部分に閉じ込める

## フォローアップノート

Implementerが範囲の外だと判断した作業と、Reviewerの提案のうち対応されなかったものは、mergeされるとPull Requestの中に埋もれる。cuminは、これをフォローアップノートに転記する。フォローアップノートは、cuminが要求Issueに付けるコメントである。mergeされたPull Requestごとに、1つ付く。Ownerは、受け入れのときに要求Issueを開けば、その要求で残った作業を見渡せる。

- きっかけは、実装Issueを閉じるPull Requestがmergeされたことである。cuminがmergeしたときも、Ownerがmergeしたときも同じに扱う
- 拾うものは2つある。Pull Requestの説明の `Follow-up` の節の文章と、対応されなかった `(non-blocking)` の指摘である
- 対応されなかった指摘とは、`cumin-reviewer` の `(non-blocking)` の指摘のうち、`Fixed` か `Answer` で始まる返答が付いていないものである。ラベルが `praise` と `note` の指摘は拾わない
- 拾うものが何もなければ、フォローアップノートを書かない
- フォローアップノートを書くのは、要求Issueが開いている間だけである。閉じた要求Issueには書かない
- sub-issueが全て閉じたときは、フォローアップノートを、受け入れの確認の依頼 (R4) より先に書く。Chief Engineerが確認を始めるときにも、Ownerが通知を受けて見に来たときにも、フォローアップノートがそろっている
- 1つのPull Requestについて、フォローアップノートは1つだけにする。コメントに目印を埋め込み、cuminが再起動しても二重に書かない
- 形式は [follow-up-note.md](../../../templates/follow-up-note.md) に従う
- フォローアップノートは記録である。Issueにはしない。Ownerは受け入れのときに一覧を見て、やりたいものを新しい要求Issueに書く。そこからは通常のフローに乗り、Chief Engineerが実装Issueに分割する

## 通知

Ownerに知らせるのは、Ownerの対応が要るときと、cuminが止まったときだけである。通知は「見に来てほしい」と伝えるだけで、やりとりはIssueとPull Requestで行う。

| 知らせるとき | 表の番号 |
|---|---|
| 分割結果の確認が必要 | R2 |
| 残りのsub-issueの確認が必要 | R6 |
| 要求が受け入れ可能になった | R7 |
| mergeの判断が必要 | I7 |
| Agentが先に進めない。指摘が残った | I2、I4、I8、I10、R2、R4 |
| 利用枠がしきい値を超えた、または使用率を読み取れなかったので、新しい着手を止めた | Q1 |
| 進められるIssueがなくなった | Q4 |

通知には、対象のIssueかPull Requestへのリンクを入れる。通知の手段は、将来差し替えられるようにする。

## 設定

設定はTOMLのファイルに書く。値をコードに埋め込まない。

秘密の値 (GitHub Appの秘密鍵、Discordのwebhookのアドレス) は、設定ファイルにもリポジトリにも書かない。Hostの macOS のKeychainに置き、cuminが起動時に読む。複数のHostから同じ値を使うようになったら、Secrets Managerに移す。

設定には、Hostに属するものと、リポジトリごとに変えてよいものがある。

- Hostに属する設定は、Hostの設定ファイルにだけ書ける。利用枠はアカウントのものであり、作業場所や秘密の値はHostのものなので、リポジトリからは変えられない
- リポジトリごとに変えてよい設定は、Hostの設定ファイルに書いた値を、対象のリポジトリの `.cumin/` で上書きできる。優先順位は、初期値、Hostの設定ファイル、リポジトリの `.cumin/` の順に強くなる
- 保護されたパスだけは、リポジトリの `.cumin/config.toml` だけで決める。強制するのがGitHub Actionsのcheckであり、Hostの設定ファイルはそこから見えないためである

| 設定 | 内容 | 初期値 | リポジトリで上書き |
|---|---|---|---|
| 対象のリポジトリ | cuminが確かめるリポジトリの一覧 | なし | できない |
| 定期確認の間隔 | GitHubを確かめる間隔 | 60秒 | できない |
| リポジトリごとに同時に進めるIssueの数 | 1つのリポジトリで、`cumin/status/planning`、`cumin/status/implementing`、`cumin/status/awaiting-checks`、`cumin/status/reviewing` にあるIssueの数の上限。Ownerの対応を待っているIssueは数えない。違うリポジトリのIssueは、並行して進めてよい | 1 | できない |
| 利用枠のしきい値 | 5h枠とweekly枠のそれぞれに、時間帯ごとに指定できる | 85% | できない |
| リセットが近いとみなす残り時間 | 5h枠の残り時間がこれを切ったら、5h枠のしきい値を100%にする | 30分 | できない |
| 作業場所 | `git worktree` を置くディレクトリ | なし | できない |
| Agentの実行時間の上限 | roleごとに指定できる。GitHub Appのtokenが発行から1時間で失効し、期限を延ばせないので、55分を超える値は指定できない | 50分 | できない |
| roleとGitHub Appの対応 | roleごとのAppのClient ID。秘密鍵がHostにあるので、Hostの設定に書く。リポジトリの持ち主 (Organization) ごとに指定できる | なし | できない |
| レビューのラウンドの上限 | これを超えて指摘が残ったら、Ownerに回す | 3 | できる |
| checkの修正を依頼する回数の上限 | これを超えたら、Ownerに回す | 3 | できる |
| roleごとのCLI | roleごとに、どのCLIとモデルでAgentを動かすか | Claude Code | できる |
| mergeの方法 | cuminがPull Requestをmergeするときの方法。squash、merge、rebaseのどれか | squash | できる |
| 保護されたパス | Agentに変更させないパスの一覧 | `.cumin/`、`CLAUDE.md`、`AGENTS.md`、`.claude/` | リポジトリだけで決める |
| riskの基準 | riskの基準を書いたMarkdownの文章。cuminは中身を解釈せず、Chief EngineerとReviewerへの指示にそのまま入れる | [Chief Engineerの要件](agents/chief-engineer.md) の表 | できる |

保護されたパスは、`.cumin/config.toml` の `protected_paths` に、文字列の配列で書く。照合の決まりは、`.gitignore` の一部と同じである。

- 末尾のほかに `/` を含まない項目 (例: `CLAUDE.md`、`.claude/`) は、どの階層にあっても当たる
- 先頭が `/` の項目、または途中に `/` を含む項目は、リポジトリの直下から数えた位置にだけ当たる
- 末尾が `/` の項目 (例: `.cumin/`) は、ディレクトリを表し、その下の全てに当たる
- ワイルドカードは使えない
- 大文字と小文字は区別しない。Hostの macOS のファイルシステムが区別しないので、`claude.md` という名前のファイルも、Agentには `CLAUDE.md` として読まれるためである

どの階層でも当たるようにするのは、Claude Codeが、サブディレクトリの `CLAUDE.md` も読むためである。リポジトリの直下だけを守ると、Implementerがサブディレクトリに `CLAUDE.md` を足して、あとに続くAgentへの指示を変えられる。

riskの基準は、TOMLの値ではなく、Markdownのファイルで上書きする。Hostでは、設定ファイルと同じディレクトリの `risk-criteria.md` に書く。リポジトリでは、`.cumin/risk-criteria.md` に書く。ファイルがあれば、その内容が、それより弱い段の基準を丸ごと置き換える。優先順位は他の設定と同じで、初期値、Hostのファイル、リポジトリのファイルの順に強くなる。

cuminは、リポジトリの `.cumin/` を、Pull Requestのブランチではなくmainから読む。`.cumin/` の変更は常に `risk/high` なので、Ownerがmergeしたものだけが効く。

## GitHub上の身元

GitHub上では `cumin-core` として振る舞う。持っている権限は、コードの読み書き (mergeに要る)、Pull Requestの読み書き、Issueの読み書きである。mainを更新できるのは、rulesetによってOwnerと `cumin-core` だけに制限する。登録の手順は [GitHub Appの登録手順](../development/github-app-setup.md) にある。

## 上位要件のテスト

| # | 場面 | 期待する結果 |
|---|---|---|
| 1 | `cumin/status/ready` の実装Issueを1つ置き、定期確認を2回以上またぐ | Implementerへの依頼は1回だけ行われる |
| 2 | blocked by のIssueが開いている実装Issueに `cumin/status/ready` を付ける | 着手しない。blocked by のIssueが閉じたら着手する |
| 3 | `risk/low` で承認され、checkの通ったPull Requestがある | cuminがmergeし、実装Issueが閉じる |
| 4 | `risk/medium` で承認され、checkの通ったPull Requestがある | mergeしない。`cumin/status/awaiting-owner-review` に替えて、Ownerに通知する |
| 5 | Agentが形式に合わない結果を返す | 同じ依頼を1回だけやり直す。それでも合わなければ `cumin/status/awaiting-owner-decision` に替えて通知する |
| 6 | 使用率がしきい値を超える | 新しい着手を止めて、1回だけ通知する。実行中のIssueは最後まで進める。`cumin quota allow` で再開する。リセット時刻を過ぎたら自動で再開する |
| 7 | 要求Issueのsub-issueが全て閉じる | Chief Engineerに、受け入れの確認が1回だけ依頼される。確認のコメントが付いたあとで、要求Issueを `cumin/status/awaiting-owner-review` に替えて、Ownerに通知する。表にFailがあっても、同じ動作になる |
| 8 | cuminを止めて、起動し直す | GitHubを確かめ直して動き始める。同じIssueを二重に依頼しない |
| 9 | 進められるIssueがなくなる | 1回だけ通知する。同じ通知を繰り返さない |
| 10 | `Follow-up` に文章があり、対応されなかった `(non-blocking)` の指摘が1つあるPull Requestをmergeする | 要求Issueに、決められた形式のフォローアップノートが1つ付く。cuminを再起動しても、同じフォローアップノートは増えない |
| 11 | `Follow-up` が None で、`(non-blocking)` の指摘が全て `Fixed` になったPull Requestをmergeする | 要求Issueにフォローアップノートは付かない |
| 12 | 要求Issueのsub-issueの一部にだけ `cumin/status/ready` を付け、それらが全て閉じる | 要求Issueを `cumin/status/awaiting-owner-review` に替えて、Ownerに1回だけ通知する。残りのsub-issueに `cumin/status/ready` を付けると、要求Issueが `cumin/status/implementing` に戻る |
| 13 | `cumin/status/ready` のsub-issueが残っている要求Issueを見直し、Chief Engineerが新しいsub-issueを足す | Ownerが新しく `cumin/status/ready` を付けるまで、要求Issueは `cumin/status/awaiting-owner-review` のままである |
| 14 | cuminが実装Issueのラベルを付け替える。Ownerが実装Issueのriskを変える。OwnerがPull Requestの側のラベルを変える | どの場合も、次の定期確認のあとで、Pull Requestの `cumin/status/*` と `risk/*` が、実装Issueと同じになる。cuminの判定は、Pull Requestのラベルに左右されない |
