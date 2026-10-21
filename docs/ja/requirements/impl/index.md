# 実装

実装Issueを Pull Request にまとめ、必須チェックが通るまでの流れを決める。cumin が Implementer を起動し、Pull Request を GitHub で確かめ、必須チェックを待ち、失敗と衝突を直させるまでである。レビューは [レビュー](../review/index.md)、マージとマージ判断は [マージ](../merge/index.md)、Agent起動に共通のことは [Agent](../agent/index.md) で決める。

## 全体像

実装Issueが実装でたどる状態を示す。灰色の状態は、ほかのカテゴリで決める。

![実装の全体像](overview.svg)

図の元ファイル: [overview.puml](overview.puml)

「Implementer が動いていない」の意味は、[Agent](../agent/index.md) の `CWK-AGENT-001` で決める。

`cumin/status/checking` は、「Implementer の作業が済み、チェックが動いている」ことを GitHub に残すための状態である。`cumin/status/implementing` のままだと、Implementer が修正の途中なのか、チェックを待っているのかを、GitHub の事実から見分けられない。チェックが動いている間は Agent が動いていないので、利用制限を使わない。Pull Request が開いただけでは、レビューに進まない。

## 要件

### 実装の Agent起動

#### `CWK-IMPL-001` 実装を始める

`cumin/status/ready` の付いた実装Issueが次のすべてを満たすとき、cumin は状態を `cumin/status/implementing` に替えてから、新しいセッションで Implementer を起動する。実装Issueを閉じる Pull Request がすでにあれば、その続きから進めさせる。

- その `cumin/status/ready` を、cumin が扱う ([権限](../access/index.md) の `CWK-ACCESS-004`)
- 実装Issueの blocked by の Issue が、すべて閉じている (`CWK-IMPL-002`)。または、土台の上で始める条件を満たす (`CWK-IMPL-023.1`)
- cumin に空きがある
- `cumin/type/owner-task` が付いていない ([ラベル](../label/index.md) の `CWK-LABEL-004`)

- 検証: `test` 4つを満たす実装Issueに、Implementer の Agent起動が新しいセッションで1回だけ起き、起動の前に状態が `cumin/status/implementing` になっている
- 検証: `test` Pull Request がすでにある実装Issueでは、新しい Pull Request を作らせず、同じ Pull Request の続きから進めさせる

---

#### `CWK-IMPL-002` 先の実装Issueを待つ

blocked by の Issue が1つでも開いている実装Issueには、cumin は着手しない。例外は、開いている blocked by の Issue が Assignee のマージ判断を待っているときで、その Pull Request の上に積んで始める (`CWK-IMPL-023`)。

- 検証: `test` blocked by の実装Issueが `cumin/status/implementing` の間は着手せず、閉じたあとの定期確認で着手する

---

#### `CWK-IMPL-003` ブランチの名前

cumin は、実装Issueごとにブランチの名前を決めて、Implementer に渡す。名前は `cumin/<実装Issueの番号>-<短い説明>` の形である。

- 検証: `test` Implementer の Agent起動で渡す事実に、この形のブランチの名前がある

---

#### `CWK-IMPL-004` 回数を数え直す

`CWK-IMPL-001` で `cumin/status/ready` から `cumin/status/implementing` に移るたびに、cumin はチェックの修正の回数 (`CWK-IMPL-012`) を0に戻す。Maintainer が介入したあとは Agent のセッションも新しくなるので、上限も数え直す。

- 検証: `test` チェックの修正を上限まで頼んで止まった実装Issueに、Maintainer が `cumin/status/ready` を付け直すと、チェックの修正をまた上限まで頼める

---

### 実装の完了判断

#### `CWK-IMPL-005` 実装の完了を判断する

Implementer が動いていないとき、cumin は Pull Request を GitHub で確かめる。cumin は、その実装Issueのために決めたブランチ (`CWK-IMPL-003`) に Pull Request が開いていて、作成者が `cumin-implementer` で、先頭のコミットが push されているとき、実装が完了したと判断する。この判断では、Pull Request をブランチと作成者で見つける。GitHub が本文の `Closes #<番号>` からリンクを作らないことがあるためである。Implementer が返した結果は、判断に使わない。

- 検証: `test` 本文の `Closes` からリンクが作られていない Pull Request でも、ブランチと作成者が合えば実装が完了したと判断する。ほかの作成者の Pull Request では、完了したと判断しない

---

#### `CWK-IMPL-006` チェックを待つ

`cumin/status/implementing` の実装Issueで、Implementer が動いておらず、実装が完了したとき、cumin は状態を `cumin/status/checking` に替える。実装Issueにその Pull Request を閉じるリンクがなければ、`cumin-core` がリンクを付け、付いたことを読み直して確かめてから替える。GitHub がすでにリンクを作っていれば、何も付けない。ほかのアクションは、このリンクで実装Issueの Pull Request を見つける。

- 検証: `test` リンクのない Pull Request で、cumin がリンクを付け、実装Issueが `cumin/status/checking` になる。リンクがすでにあれば、付け直さない

---

#### `CWK-IMPL-007` 実装をやり直す

`cumin/status/implementing` の実装Issueで、Implementer が動いておらず、実装が完了しておらず、Implementer の質問のコメントがないとき、cumin は Implementer をもう一度起動して、続きから進めさせる。やり直すのは、1つの `cumin/status/implementing` の間に1回だけである。状態は替えない。

- 検証: `test` Pull Request を開かずに終わった実装のあとで、Implementer の Agent起動がもう一度だけ起き、状態は `cumin/status/implementing` のままである

---

#### `CWK-IMPL-008` 実装を止める

`cumin/status/implementing` の実装Issueで、Implementer が動いておらず、次のどれかが成り立つとき、cumin は状態を `cumin/status/awaiting-decision` に替えて、通知する。質問のコメントでないときは、止めた理由を実装Issueのコメントに書く。

- Implementer の質問のコメントが、実装Issueが `cumin/status/implementing` になったあとに書かれている。Implementer の質問では、やり直さない。実装に要る要件が足りないことが多く、Maintainer が Issue を書き直す必要があるためである
- やり直し (`CWK-IMPL-007`) のあとも、実装が完了していない
- 衝突の解消 (`CWK-IMPL-015`) を頼んだあとも、先頭のコミットが頼む前のままである。先頭のコミットの時刻と、実装Issueに最新の `cumin/status/implementing` が付いた時刻を比べて決める

- 検証: `test` Implementer が `blocked` を返すと、やり直さずに `cumin/status/awaiting-decision` になる
- 検証: `test` やり直しのあとも実装が完了しないと、理由のコメントが付いて `cumin/status/awaiting-decision` になる
- 検証: `test` 衝突の解消のあとも先頭のコミットが変わらないと、衝突の解消を繰り返さずに `cumin/status/awaiting-decision` になる

---

### 必須チェック

#### `CWK-IMPL-009` 必須チェックの一覧

必須チェックは、main ブランチに効く ruleset の "Require status checks to pass before merging" に登録されたチェックである。cumin は、その一覧を GitHub の ruleset から読む。今そのコミットに付いているチェックの一覧では決めない。push の直後はチェックがまだ1つも現れておらず、チェックがないのか、これから現れるのかを見分けられないためである。

- 検証: `test` ruleset に登録したチェックだけを待ち、登録していないチェックの失敗では止まらない

---

#### `CWK-IMPL-010` 必須チェックが通る

必須チェックが通るとは、一覧のすべてのチェックが、Pull Request の先頭のコミットで、結論 `success`、`skipped`、`neutral` のどれかを返したことである。GitHub も、この3つをマージを止めない結論として扱う。一覧が空なら、すぐに通る。

- 検証: `test` 3つの結論はどれも通ったと扱い、ほかの結論は通ったと扱わない。一覧が空のリポジトリでは、`cumin/status/checking` を通って、次の定期確認でレビューに進む

---

#### `CWK-IMPL-011` 判定の順番

`cumin/status/checking` の実装Issueでは、1回の定期確認で、衝突の解消 (`CWK-IMPL-015`)、チェックの結果 (レビューに進む、`CWK-IMPL-012`、`CWK-IMPL-013`)、`CWK-IMPL-014` の順に判定し、最初に成り立ったものだけを行う。

- 検証: `test` 衝突していて、チェックの待ち時間も過ぎた実装Issueでは、衝突の解消だけが起きる

---

#### `CWK-IMPL-012` チェックの失敗を修正する

`cumin/status/checking` の実装Issueで、必須チェックのどれかが失敗し、チェックの修正を頼んだ回数が設定の上限 (`max_check_fix_requests`、初期値は3回) に達していないとき、cumin は状態を `cumin/status/implementing` に替えて、Implementer の直前のセッションで修正を頼む。失敗したチェックの内容を添える。

- 検証: `test` 必須チェックが失敗すると、失敗したチェックの内容を添えて、直前のセッションで Implementer の Agent起動が1回起きる

---

#### `CWK-IMPL-013` チェックの失敗で止める

`cumin/status/checking` の実装Issueで、必須チェックのどれかが失敗し、チェックの修正を頼んだ回数が上限に達しているとき、cumin は状態を `cumin/status/awaiting-decision` に替えて、通知する。コメントと通知には、失敗したチェックの名前と、頼んだ回数を書く。

- 検証: `test` 上限までチェックの修正を頼んでも失敗すると、`cumin/status/awaiting-decision` になり、通知は1回だけである

---

#### `CWK-IMPL-014` チェックが来ないときに止める

`cumin/status/checking` の実装Issueで、設定の待ち時間 (`checks_wait_time`、初期値は60分) を過ぎても、必須チェックのどれかが先頭のコミットで結果を返していないとき、cumin は状態を `cumin/status/awaiting-decision` に替えて、通知する。

- 待ち時間は、実装Issueに最新の `cumin/status/checking` が付いた時刻と、先頭のコミットの時刻のうち、遅いほうから数える。待っている間に誰かがブランチに push すると、そこから数え直す。
- 実装Issueを閉じる開いている Pull Request がないときも、ラベルの時刻から待ち時間を過ぎたら止める。
- コメントと通知には、先頭のコミット、結果を返していない必須チェック、待った時間を書く。Pull Request がないときは、そのことと待った時間を書く。チェックが来ない理由は調べない。理由は Maintainer が調べる。

- 検証: `test` 待ち時間を過ぎても結果を返さない必須チェックがあると、先頭のコミット、そのチェック、待った時間を添えて、通知が1回だけ届く
- 検証: `test` 開いている Pull Request がなくなった実装Issueでも、待ち時間を過ぎると、そのことを添えて止まる

---

### 衝突の解消

#### `CWK-IMPL-015` 衝突の解消

`cumin/status/checking` か `cumin/status/awaiting-merge-decision` の実装Issueで、GitHub が Pull Request をその base (ふつうは main ブランチ、積んだ Pull Request では土台のブランチ) と衝突していると返したとき (GraphQL の `mergeable` が `CONFLICTING`)、cumin は状態を `cumin/status/implementing` に替えて、Implementer の直前のセッションで衝突の解消を頼む。`cumin/status/merging` からの衝突の解消は、[マージ](../merge/index.md) で決める。

- `mergeable` が `UNKNOWN` のときは、GitHub がまだ計算しているので、その定期確認では判定しない。`UNKNOWN` のまま待ち時間を過ぎたら、`CWK-IMPL-014` で止まる。
- 衝突の解消は、チェックの修正の回数に数えない。衝突は Implementer の誤りではなく、並行して進むほかの Pull Request のマージで起きるためである。
- 衝突したまま待たないのは、GitHub が衝突のある Pull Request では `pull_request` のワークフローを動かさず、チェックがいつまでも結果を返さないためである。

- 検証: `test` 衝突した Pull Request の実装Issueで、Implementer に衝突の解消を1回だけ頼み、チェックの修正の回数は増えない
- 検証: `test` `mergeable` が `UNKNOWN` の間は、衝突の解消を頼まない

---

### 土台の上に積む

Assignee のマージ判断を待つ Pull Request を「土台」、その上で始めた実装Issueの Pull Request を「積んだ Pull Request」と呼ぶ。Assignee が判断するまで、その Pull Request に依存する実装Issueが止まらないように、cumin は土台がマージされたものとみなして、その上で次の実装を始める。土台を待つこと、土台のマージのあとに main ブランチを取り込むこと、土台より先にマージしないことは、[マージ](../merge/index.md) で決める。

#### `CWK-IMPL-023` 土台の上で始める

cumin は、条件を満たす実装Issueを、土台の上に積んで始める。積んだあとも、必須チェックとレビューは、ほかの Pull Request と同じに通る。

##### `CWK-IMPL-023.1` 着手の条件

`cumin/status/ready` の実装Issueで、`CWK-IMPL-001` の blocked by のほかの条件を満たし、次のすべてが成り立つとき、cumin は、土台の上に積んで実装を始める。

- 開いている blocked by の Issue が1つだけで、それが `cumin/status/awaiting-merge-decision` である
- その Issue の Pull Request (土台) の base が、main ブランチである
- 対象のリポジトリが、マージのあとにブランチを自動で消す設定 (`deleteBranchOnMerge`) である。ブランチが消えたときにだけ、GitHub が積んだ Pull Request の base を main ブランチに替えるためである。この設定がないリポジトリでは、積まずに今までどおり待つ

`cumin/status/awaiting-merge-decision` は、Reviewer が土台の今の先頭のコミットを承認したことを表す。Reviewer が承認する前の Pull Request の上には積まない。

- 検証: `test` blocked by の実装Issueが `cumin/status/awaiting-merge-decision` で、その Pull Request の base が main ブランチなら、着手する。`cumin/status/reviewing` のとき、`deleteBranchOnMerge` が偽のとき、開いている blocked by が2つあるときは着手しない

---

##### `CWK-IMPL-023.2` ブランチと base

積んで始めるとき、cumin は Implementer に、土台のブランチを渡す。Implementer は、新しいブランチを土台のブランチから切り、Pull Request の base を土台のブランチにする。差分に、積んだ変更だけが出るようにするためである。cumin は、Pull Request の base が main ブランチでないことで、積んだ Pull Request を見分ける。ラベルも draft も使わない。

- 検証: `test` 積んで始める Agent起動で渡す事実に、土台のブランチがある
- 検証: `demo` 積んで始めた実装Issueの Pull Request の base が土台のブランチで、差分に積んだ変更だけがある

---

##### `CWK-IMPL-023.3` 1段だけ積む

積むのは1段だけである。土台の base が main ブランチであることを条件にするので、積んだ Pull Request の上には積まない。

- 検証: `test` blocked by の Issue の Pull Request が積んだ Pull Request (base が main ブランチでない) なら、その上には着手しない

---

##### `CWK-IMPL-023.4` 土台の変更は追わない

Assignee が土台に `REQUEST_CHANGES` を出して土台が変わっても、cumin は積んだ側を更新しない。土台との衝突だけを、衝突の解消 (`CWK-IMPL-015`) で解く。土台が動くたびに更新すると、そのたびに Implementer の実行とレビューが1回ずつ要るためである。積んだブランチは、土台がマージされるまで、古い土台の上にある。

- 検証: `test` 土台に新しいコミットが入っても、積んだ実装Issueに Agent起動は起きない。土台と衝突したときだけ、衝突の解消を頼む

---

### Implementer の成果物

#### `CWK-IMPL-016` Pull Request

Implementer は、1つの実装Issueにつき、渡された名前のブランチに Pull Request を1つ開く。説明は [pull-request.md](../../../../templates/pull-request.md) の形式で書き、本文に `Closes #<実装Issueの番号>` を書く。続きや修正のときは、新しい Pull Request を作らず、同じ Pull Request にコミットを積む。

- 検証: `demo` `cumin/status/ready` の実装Issueで実装すると、渡した名前のブランチに Pull Request が1つ開き、本文に `Closes #<番号>` がある
- 検証: `demo` チェックの修正と、マージ判断で Assignee が出した `REQUEST_CHANGES` のあとの続きで、新しい Pull Request は作られず、同じ Pull Request にコミットが積まれる

---

#### `CWK-IMPL-017` 範囲の外の作業

Implementer は、実装Issueに書かれていない範囲に手を広げない。自分で気づいた範囲の外の作業は、Pull Request の説明の `Follow-up` に書き、Issue は作らない。マージのあとに、cumin がフォローアップノートとして要求Issueに書く ([マージ](../merge/index.md))。

- 検証: `inspection` レビューで、Pull Request の変更が実装Issueの範囲の中にあり、範囲の外の作業が `Follow-up` にあることを確かめる

---

#### `CWK-IMPL-018` Pull Request の図

Implementer は、Pull Request の「Where this fits」の図を直すときは、直した図をブランチ `cumin/diagrams` に新しいファイルとして足す。Pull Request のブランチには図のファイルを置かない。ただし、設計文書の図を変えるときは、Pull Request のブランチで変える。

- 検証: `demo` 実装で位置づけが変わった Pull Request で、直した図が `cumin/diagrams` にあり、Pull Request のブランチには、設計文書の図のほかに図のファイルがない

---

#### `CWK-IMPL-019` Issue を変えない

Implementer は、実装Issueと要求Issueの本文を書き換えない。

- 検証: `demo` 実装のあとで、実装Issueと要求Issueの本文が変わっていない

---

#### `CWK-IMPL-020` 待たずに終わる

Implementer は、Pull Request を開いて push したら、レビューもチェックも待たずに実行を終える。チェックとレビューを待つのは cumin である。

- 検証: `inspection` Implementer の role のファイルが、チェックとレビューを待たないよう求めていることを確かめる

---

### 保護されたパスのチェック

#### `CWK-IMPL-021` 保護されたパスのチェック

保護されたパスが変わっていないことは、cumin ではなく、GitHub Actions のチェックで確かめる。チェックが失敗したときは、ほかの必須チェックと同じく `CWK-IMPL-012` に進む。

##### `CWK-IMPL-021.1` 保護されたパスの一覧

保護されたパスの一覧は、対象のリポジトリの `.cumin/config.toml` の `protected_paths` に書く。ファイルかキーがなければ、初期値の `.cumin/`、`CLAUDE.md`、`AGENTS.md`、`.claude/`、`REVIEW.md` を使う。`REVIEW.md` を含めるのは、Agent が書き換えると、Reviewer の判断が変わるためである ([レビュー](../review/index.md) の `CWK-REVIEW-017`)。

- 検証: `test` `protected_paths` のないリポジトリで、`REVIEW.md` を変える bot の Pull Request のチェックが失敗する

---

##### `CWK-IMPL-021.2` 変更ファイルと突き合わせる

対象のリポジトリに GitHub Actions のワークフローを置く。ワークフローは、Pull Request の変更ファイルの一覧を保護されたパスの一覧と突き合わせ、1つでも当たれば失敗する。

- 検証: `demo` 保護されたパスを変える Pull Request を bot が開くと、チェックが失敗する

---

##### `CWK-IMPL-021.3` 必須チェックにする

このチェックを、main ブランチの ruleset で必須チェックに登録する。

- 検証: `inspection` 対象のリポジトリの ruleset に、このチェックが必須チェックとして登録されている

---

##### `CWK-IMPL-021.4` bot の Pull Request だけに掛ける

チェックの job は、Pull Request の作成者が bot (GitHub App など) のときだけ動く。作成者が人のときは job が飛ばされ、GitHub は `skipped` として扱うので、人の Pull Request はこのチェックで止まらない。条件に GitHub App の名前は使わない。名前を間違えると job が飛ばされて通った扱いになり、保護が黙って効かなくなるためである。

- 検証: `demo` 人が保護されたパスを変える Pull Request を開くと、チェックが `skipped` になる
- 検証: `inspection` ワークフローの条件が、GitHub App の名前ではなく、作成者が bot かどうかで決まっている

---

##### `CWK-IMPL-021.5` main ブランチの一覧を読む

ワークフローは、保護されたパスの一覧を、Pull Request のブランチではなく main ブランチから読む。一覧のある `.cumin/` も、保護されたパスに含める。Implementer は、自分のブランチなら保護されたパスのファイルもコミットできる。保護は、このチェックが失敗してマージできないことで効く。Pull Request のブランチから一覧を読むと、Implementer が同じ Pull Request で一覧から `.cumin/` と自分の変更を外せば、書き換えた一覧で判定されて、チェックが通ってしまう。`.cumin/` を一覧に含めるのは、一覧を書き換える Pull Request がマージされて、後の Pull Request の保護が弱まることを防ぐためである。

- 検証: `demo` Pull Request のブランチで一覧から自分の変更を外しても、チェックが失敗する

---

### Implementer が判断を求めるとき

#### `CWK-IMPL-022` 推測で実装しない

Implementer は、次のどれかに当たったら、推測で進めずに `blocked` を返す。cumin は、やり直さずに止める (`CWK-IMPL-008`)。

##### `CWK-IMPL-022.1` 足りない要件

実装に要る要件が足りないか、要件どうしが食い違っているときは、止まる。理由には、何が決まっていないかを書く。

- 検証: `demo` 要件の足りない実装Issueで実装すると、結果は `blocked` で、何が決まっていないかが理由に書いてある

---

##### `CWK-IMPL-022.2` 変えられないファイル

保護されたパスか `.github/workflows` の変更が要るときは、変えずに止まる。理由には、そのパスを書く。

- 検証: `demo` 保護されたパスの変更が要る実装Issueで実装すると、そのパスは変わらず、結果は `blocked` で、理由にそのパスが書いてある

---

##### `CWK-IMPL-022.3` 大きすぎる実装Issue

実装Issueが、1つの Pull Request に収まらない大きさだと分かったときは、止まる。

- 検証: `demo` 1つの Pull Request に収まらない実装Issueで実装すると、結果は `blocked` で、分け方が理由に書いてある

---

##### `CWK-IMPL-022.4` 足りない先の作業

blocked by の Issue で終わっているはずの作業が、実際には足りていないときは、止まる。積んで始めた実装Issue (`CWK-IMPL-023`) では、土台のブランチにあるはずの作業である。

- 検証: `demo` 先の実装Issueの成果が main ブランチにない状態で実装すると、結果は `blocked` で、足りない作業が理由に書いてある

---

## 受け入れテスト

| # | 場面 | 期待する結果 |
|---|---|---|
| 1 | Maintainer が実装Issueに `cumin/status/ready` を付ける。Implementer が Pull Request を開き、必須チェックが通る | 実装が始まり (`CWK-IMPL-001`)、実装が完了したと判断されて `cumin/status/checking` になり (`CWK-IMPL-005`、`CWK-IMPL-006`)、チェックが通るとレビューに進む (`CWK-IMPL-010`) |
| 2 | 必須チェックが失敗し、Implementer が直す | 同じセッションで修正を頼み (`CWK-IMPL-012`)、同じ Pull Request に修正が積まれ (`CWK-IMPL-016`)、`cumin/status/checking` に戻ってチェックが通る |
| 3 | `cumin/status/checking` の間に、ほかの Pull Request のマージで衝突する | `UNKNOWN` の間は待ち、`CONFLICTING` になると衝突の解消を1回だけ頼む (`CWK-IMPL-015`)。チェックの修正の回数は増えない |
| 4 | 必須チェックが待ち時間を過ぎても結果を返さない | `cumin/status/awaiting-decision` になり、先頭のコミット、そのチェック、待った時間を添えて通知が1回届く (`CWK-IMPL-014`) |
| 5 | 保護されたパスの変更が要る実装Issueで実装する | Implementer が `blocked` を返し (`CWK-IMPL-022.2`)、やり直さずに `cumin/status/awaiting-decision` になる (`CWK-IMPL-008`) |
| 6 | Maintainer が止まった実装Issueを直し、`cumin/status/ready` を付け直す | 新しいセッションで続きから進み、新しい Pull Request は作られない (`CWK-IMPL-001`、`CWK-IMPL-016`)。チェックの修正の回数は0から数え直す (`CWK-IMPL-004`) |
| 7 | 先の実装Issueが Assignee のマージ判断を待っている間に、それに依存する実装Issueに `cumin/status/ready` が付いている | 土台のブランチから切ったブランチで実装が始まり、Pull Request の base が土台のブランチになる (`CWK-IMPL-023.1`、`CWK-IMPL-023.2`)。そのあとは [マージ](../merge/index.md) の土台を待つ流れに進む |

## 未決事項

なし
