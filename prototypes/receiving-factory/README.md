# MAW 收货工坊 · RF-001

独立 Three.js 交互原型。所有人物、货车、货箱、月台、工位和货架均为原创程序化几何体；Three.js 的许可见 node_modules/three/LICENSE。无外部字体、贴图、模型或 CDN 请求。

```sh
cd prototypes/receiving-factory
npm ci
npm run dev -- --port 7431
npm test
npm run build
```

打开 http://127.0.0.1:7431 。支持拖动旋转、滚轮缩放、点击货车/工人、批次选择、暂停、1/2/4倍速、解除阻塞、重播及相机复位。尊重系统减弱动态设置，默认暂停，可手动播放。

两个 ware（4 件与 12 件货物），三个 Agent，各货物状态为 queued → carrying → processing → storing → stored。第二批第三件货物会阻塞，点击“解除阻塞”继续。只有全部入库才允许车辆离厂；卸空车辆不会提前完成。动画未运行时，任务进度也不推进。

## 与当前 maw 工作的配合

仅写入 prototypes/receiving-factory/**，不修改已有 board、共享依赖、路由、后端、任务定义或其他 worktree。所有数据明确为模拟，页面不访问 maw API、不执行调度或修改真实任务。simulation.js 是独立状态层，后续可替换为权威快照/事件驱动；真实接入时不得以动画时间写回业务完成。

## 素材目录

main.js 中 truck：两种车型；person：三套工服及行走/抱箱/求助动作；cargo：货箱/封条/标签；场景：月台、停车位、路灯、植物、处理台、成果货架。共享材质，像素比上限 2。首版未加入音效、复杂骨骼或导出 GLB。
