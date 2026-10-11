# 図の描き方

図はPlantUMLで描く。元ファイル (`.puml`) をGitで管理し、SVGに書き出して文書に埋め込む。元ファイルは、その図を使う文書と同じディレクトリに置く。

## 図のラベルは英語

`.puml` には英語だけを書く。ラベルは、識別子 (`cumin/status/ready`、request the review) と、短い英語の語句にする。コメントも英語にする。図は、IssueやPull Requestにそのまま画像として貼られ、GitHubに書くものは全て英語だからである。

図の読み方や、ラベルだけでは分からない意味は、図を埋め込んだ文書の日本語の本文で説明する。

状態遷移図では、`issue-states.md` の1つの行を1本の矢印にし、ラベルをその行の遷移の名前 (request the split、request the review など) で始める。1本の矢印に2つの行を書かない。こうすると、IssueやPull Requestで、1つの行だけに色を付けて示せる。状態を変えない行 (「copy the labels to the pull request」) は、矢印ではなくメモで示す。

## 文書への埋め込み

SVGを埋め込んだ直後に、元ファイルへのリンクを必ず書く。人もAgentも、図の元の記述をいつでも開けるようにするためである。

```markdown
![要求Issueの状態遷移](requirement-issue-states.svg)

図の元ファイル: [requirement-issue-states.puml](requirement-issue-states.puml)
```

## IssueとPull Requestに貼る画像のアドレス

AgentがIssueとPull Requestに図を貼るときのアドレスは、リポジトリが公開か非公開かで決まる。設計は [cumin-core.md](../designs/cumin-core.md) の「非公開のリポジトリ」にある。

| リポジトリ | 画像のアドレス | 読める人に表示される場所 |
|---|---|---|
| 公開 | `https://raw.githubusercontent.com/<owner>/<repo>/<commit>/<path>.svg` | WebとGitHubアプリ (実測 115) |
| 非公開 | 相対リンク `../blob/<commit>/<path>.svg?raw=true` | Webだけ (実測 153) |

- どちらも、コミットを指定する。図はあとから変わらない。
- 公開か非公開かは、Agentが `gh api repos/<owner>/<repo> --jq .private` で読む。
- 非公開のリポジトリでは、GitHubアプリに図が表示されない。図を見るには、Webで開く。リポジトリを読めない人には、何も見えない。
- Agentへの指示は、[templates/writing-rules.md](../../../templates/writing-rules.md) の13、[roles/planner.md](../../../roles/planner.md)、[templates/implementation-issue.md](../../../templates/implementation-issue.md)、[templates/pull-request.md](../../../templates/pull-request.md) にある。
- テスト `TestImageAddress_EveryInstructionAndSkillUsesTheFormsOfTheDesign` ([internal/agent/instruction_test.go](../../../internal/agent/instruction_test.go)) は、roleの指示、スキル、テンプレートが、この2つ以外の形のアドレスを書くと失敗する。片方の形だけを書いても失敗する。

実測の番号は、[measured-constraints.md](../evidence/measured-constraints.md) の行である。

## SVGの書き出し

リポジトリの直下で次を実行する。`docs/` の下にある全ての `.puml` を、同じディレクトリにSVGとして書き出す。

```sh
scripts/render-diagrams.sh
```

1つの `.puml` だけを、好きな場所のSVGに書き出すこともできる。`docs/` の外の図 (例えば、PlannerがIssueのために描く図) に使う。

```sh
scripts/render-diagrams.sh <in.puml> <out.svg>
```

必要なものはDockerだけである。スクリプトは [tools/plantuml/Dockerfile](../../../tools/plantuml/Dockerfile) からイメージを作り、そのイメージで書き出す。

`.puml` を直したら、SVGも書き出し直してコミットする。

## 書き出し忘れの確認

`go test ./...` に含まれるテスト `TestDiagrams_SVGMatchesSource` ([tools/plantuml/diagrams_test.go](../../../tools/plantuml/diagrams_test.go)) が、`docs/` の下の全ての `.puml` について、隣のSVGが今の `.puml` から書き出されたものかを確かめる。

PlantUMLは、図の元の記述をSVGの中に `<!--SRC=[...]-->` というコメントで書き込む。形式は、UTF-8、Deflate、PlantUML独自のbase64の順である ([Text Encoding](https://plantuml.com/text-encoding))。コメントには、最初の `@startuml` の行と最後の `@enduml` の行が入らない。XMLのコメントには `--` を書けないので、PlantUMLはコメントの中の `--` を `- -` と書く。空白は符号化に使う文字ではないので、テストは空白を取り除いてから元に戻す。テストは、`.puml` の最初の行が `@startuml <ファイル名>` で、最後の行が `@enduml` であることを確かめ (この名前が、書き出すSVGのファイル名になる)、その間の行を、元に戻したコメントと比べる。書き出しはしないので、Dockerは要らない。

テストが見つけるものと、見つけないもの:

| 見つける | 見つけない |
|---|---|
| `.puml` を直したのに、SVGを書き出し直していない | 手元のフォントなど、このスクリプト以外で書き出したSVG |
| `.puml` の隣にSVGがない | SVGの本体を手で直したこと |
| SVGに元の記述のコメントがない | |

テストが見つけないものは、次の「書き出し直して比べる確認」が見つける。

## 書き出し直して比べる確認

次のコマンドは、`<base>` (コミット、ブランチ、タグ) と作業ツリーの間で変わった図だけを書き出し直し、作業ツリーのSVGと1バイトずつ比べる。

```sh
scripts/render-diagrams.sh --check <base>
```

- 対象は、`docs/` の下で `.puml` かSVGが `<base>` と違う図である。Gitがまだ管理していないファイルも含む。
- [tools/plantuml/](../../../tools/plantuml/) の下のファイル (書き出す側) が `<base>` と違うときは、`docs/` の下の全ての図を対象にする。
- 対象の図がないときは、そう表示して終了コード0で終わる。Dockerは使わない。
- 書き出し先は一時ディレクトリである。作業ツリーのファイルは変えない。

| 終了コード | 意味 |
|---|---|
| 0 | 対象の全てのSVGが、書き出し直した結果と同じ |
| 1 | 違うSVGか、ないSVGがある。そのSVGのパスを標準エラーに1行ずつ書く |
| 2 | 引数が違う |

CIのジョブ `diagrams` ([ci.yml](../../../.github/workflows/ci.yml)) が、Pull Requestごとにこの確認を実行する。`<base>` は、Pull Requestのbaseのブランチである。変わった図がなければ、書き出さずに通る。

全ての図を書き出し直すと時間がかかるので、変わった図だけを比べる。Issueの図 (ブランチ `cumin/diagrams`) は `.puml` を持たないので、対象ではない。

2つの確認が見つけるもの:

| 起きたこと | `TestDiagrams_SVGMatchesSource` | `--check <base>` |
|---|---|---|
| `.puml` を直したのに、SVGを書き出し直していない | 見つける | 見つける |
| `.puml` の隣にSVGがない | 見つける | 見つける |
| SVGに元の記述のコメントがない | 見つける | 見つける |
| 手元のフォントなど、このスクリプト以外で書き出したSVG | 見つけない | 見つける |
| SVGの本体を手で直したこと | 見つけない | 見つける |
| `<base>` より前から違っていたSVG (書き出す側が変わっていないとき) | 見つける (元の記述が違うときだけ) | 見つけない |
| 要るもの | Goだけ | DockerとGit |

## 公式のイメージをそのまま使わない理由

公式の `plantuml/plantuml` のイメージには、日本語のフォントが入っていない。PlantUMLは文字の幅を測ってSVGに `textLength` として書き込むので、日本語のフォントがないと幅を短く測ってしまう。そのSVGを開くと、表示する側が文字を短い幅に詰め込むため、日本語の文字が重なって読めなくなる。

`tools/plantuml/Dockerfile` は、公式のイメージに Noto Sans CJK を足している。2026-09-19に確かめた値は次の通り。

| 文字列 | フォントなしで測った幅 | フォントありで測った幅 |
|---|---|---|
| 「提出済み」(14pxの全角4文字。正しくは56px) | 33.6px | 56.0px |

PNGへの書き出しでは、この問題は起きない。文字の幅を表示する側に任せるSVGだけで起きる。

## VS Codeでのプレビュー

VS CodeのPlantUML拡張でプレビューするには、HostにJavaとGraphvizが要る。

- シーケンス図は、Graphvizがなくてもプレビューできる。
- 状態遷移図など、配置をGraphvizに任せる図は、Graphviz (`dot` コマンド) がないとプレビューできない。

Graphvizは次のコマンドで入る。

```sh
brew install graphviz
```

プレビューは手元で形を確かめるためのものである。文書に埋め込むSVGは、必ず `scripts/render-diagrams.sh` で書き出す。手元のフォントで書き出すと、書き出す環境によってSVGが変わってしまう。
