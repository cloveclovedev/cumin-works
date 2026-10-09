# 既存のプロダクトにcuminを入れる

すでに動いているプロダクトのリポジトリに、cuminを入れるときに決めることと、その順番。手順そのものは [セットアップの手順](../development/setup-guide.md) にある。この文書は、手順の前とあとで迷うところだけを書く。

## 前提

- 対象のリポジトリの持ち主は、GitHubのOrganizationである。個人のアカウントのリポジトリは、まだ対象にしていない。
- 対象のリポジトリは公開 (public) である。Freeプランのprivateのリポジトリでは、rulesetが使えないので、まだ動かさない。
- cumin本体を動かすHostは、macOSのマシンである。

## 1. どのリポジトリで動かすか

| 選び方 | 向いているとき |
|---|---|
| 今のリポジトリにそのまま入れる | CIが整っていて、既存のrulesetやIssueと共存できるとき |
| 作り直し用の新しいリポジトリを作る | CIの作りが合わない、構成を一から変える、既存のIssueやrulesetに手を入れたくないとき |

新しいリポジトリにしても、前のリポジトリは読めるようにしておける。公開のリポジトリなら、Agentは認証なしでcloneできる。Agentの環境は対象のリポジトリ用のtokenを付けているので、それを外して読む。

```sh
git -c http.https://github.com/.extraheader= clone --depth 1 https://github.com/<owner>/<前のリポジトリ> <作業ディレクトリの外の一時ディレクトリ>
```

この1行と「前のリポジトリは読むだけ」という決まりを、新しいリポジトリの `CLAUDE.md` に書く。

## 2. CIを、どのPull Requestでも必ず動く形にする

cuminは、必須のcheckが先頭のコミットで全て通るのを待ってから、レビューに進む。

- 必須のcheckにするjobは、どのPull Requestでも必ず動くものにする。workflowの `on.pull_request.paths` で絞ると、関係のないPull Requestではworkflowごと飛ばされ、checkが保留のまま残って、mergeとcuminの両方が止まる。
- 変わったパスでjobを分けたいときは、必ず動く1つのjobを必須のcheckにし、その中で、またはその後ろのjobの `if` で、動かすかどうかを決める。条件で飛ばされたjobは、通った扱いになる。
- workflowは、cuminを入れる前にMaintainerが用意する。ImplementerのAppには、workflowを変える権限がない。

## 3. 保護されたパスを決める

`scripts/setup-repo.sh` が入れる `.cumin/config.toml` の `protected_paths` は、Agentが変えてはいけないパスの一覧である。初期値は `.cumin/`、`CLAUDE.md`、`AGENTS.md`、`.claude/` である。

足すかどうかの目安:

| パス | 目安 |
|---|---|
| 要件の文書 | 足す。要件を書くのはMaintainerだけである |
| デプロイの設定、本番の設定、秘密の値の参照 | 足す。誤った変更を、mergeの前に止めたい |
| 開発の対象そのもの (コード、テスト、設計の文書) | 足さない。Agentの仕事である |

`.github/workflows/` は、一覧になくても、ImplementerのAppの権限で変えられない。

## 4. 既存のrulesetと突き合わせる

`scripts/setup-repo.sh` は、名前で探したrulesetを作るか、内容を合わせる。同じブランチに、別の名前の既存のrulesetがあると、両方が効く。

- 既存のrulesetの例外 (bypass list) に、cumin-coreのAppがいないと、cuminのmergeが止まる。
- 既存のrulesetが必須のcheckを持っていると、そのcheckも必須になる。2のとおり、必ず動くものか確かめる。

`scripts/setup-repo.sh --dry-run` で、当てるrulesetを先に見られる。

## 5. Hostに足して動かす

1. 4つのAppのインストールに、リポジトリを足す。これは画面で行う。リポジトリを足すAPIは、classic personal access tokenでしか使えず、`gh` のtokenでは使えない ([調査・実測で確定した制約](../evidence/measured-constraints.md) の126)。
2. Hostの設定の `repositories` に足し、cuminを再起動する。設定だけを変えたときは、`launchctl kickstart -k` で足りる。`scripts/install.sh --restart` は、バイナリを作り直して入れ替えてから再起動する。実行中のAgentを取り消さずにバイナリを入れ替えるときは、`scripts/replace-binary.sh` を使う ([Hostの一般の道具](host-tools.md) の「バイナリを入れ替える」)。
3. ログに、そのリポジトリの `poll` が出ることを確かめる。

## 6. 最初の要求Issue

- 最初の要求Issueは、Maintainerが書く。[要求Issueのテンプレート](../../../templates/requirement-issue.md) に従い、`cumin/type/requirement` と `cumin/status/ready` を付ける。
- 最初は、小さく、結果を確かめやすいものにする。Plannerの分割、Implementerの実装、Reviewerのレビュー、mergeまでの流れを、1回通して見るためである。
- 要求Issueどうしの順番は、blocked by で決める。先の要求Issueが閉じるまで、後の分割は始まらない。
- 依存のないIssueのうち、先に進めたいものには、優先度のラベルを付ける。Organizationが既に優先度のラベルを使っているなら、その名前を `.cumin/config.toml` の `priority_labels` に書く ([設定の一覧](../development/configuration.md) の「優先度のラベル」)。

## 7. Maintainerのセッションにskillを入れる

cuminと並んで、自分のClaude Codeのセッションで計画やPull Requestを見るMaintainerは、対象のリポジトリにplugin `cumin-maintainer` を入れる。セッションが、cuminの代わりに動かないための決まりと、cuminとの働き方を知る。手順は [Maintainerのセッションにskillを入れる](maintainer-skills.md) にある。入れなくても、cuminは同じように動く。
