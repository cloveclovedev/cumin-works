# 図の描き方

図はPlantUMLで描く。元ファイル (`.puml`) をGitで管理し、SVGに書き出して文書に埋め込む。元ファイルは、その図を使う文書と同じディレクトリに置く。

## SVGの書き出し

リポジトリの直下で次を実行する。`docs/` の下にある全ての `.puml` を、同じディレクトリにSVGとして書き出す。

```sh
scripts/render-diagrams.sh
```

必要なものはDockerだけである。スクリプトは [tools/plantuml/Dockerfile](../../../tools/plantuml/Dockerfile) からイメージを作り、そのイメージで書き出す。

`.puml` を直したら、SVGも書き出し直してコミットする。

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
