# Swift / Xcode 适配

从 @main、App、WindowGroup、Scene、UIKit AppDelegate / SceneDelegate 追踪真实入口。区分页面、sheet、导航目的地与可复用 View；多个入口或 demo target 分别归属，不漏掉辅助窗口，也不把每个 View 都当作页面。

读取 .pbxproj、.xcscheme、.xcworkspacedata、.xcconfig、.entitlements，核对 target、源码成员、资源和构建配置。工程配置属于扫描源码；.app、.framework、.xcframework、Build、DerivedData 为产物，不能用于源码覆盖证明。

沿 UI 事件→状态/模型→服务调用→持久化或 URLSession 追踪。Metal 渲染与手势组件属于各自功能链，纯 GPU 帧不是完整 App 界面截图。现有截图与当前 View 控件核对；不能在只读分析中启动模拟器或编译来补图，缺图记 preview 缺口。
