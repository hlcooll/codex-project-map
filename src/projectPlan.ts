export type PlanKind = 'module' | 'feature' | 'page' | 'api' | 'component' | 'storage' | 'test' | 'file'
export interface PlanEvidence { file: string; match: string; line: number; valid: boolean; reason?: string; digest?: string }
export type UIPreviewKind = 'capture'|'render'|'reference'
export interface PlanCapture {label:string;url:string;kind?:UIPreviewKind;source?:string;assetId?:string;reason?:string;mode?:'light'|'dark'}
export interface UIAsset {id:string;file:string;url:string;label:string;source:'project'|'capture';references:{file:string;line:number;text:string}[]}
export interface ProjectUI {total:number;withPreview:number;withCapture:number;assets:UIAsset[];warnings:string[]}
export const previewKindLabel:Record<UIPreviewKind,string>={capture:'完整界面',render:'渲染输出',reference:'历史参考'}
export const previewSourceLabel=(source?:string)=>source==='codex'?'Codex 关联':source==='manual'?'手动关联':source==='manifest'?'项目映射':'预览素材'
export interface PlanPreview { group: string; screen: string; captures: PlanCapture[]; demo?: string }
export interface PlanNode { id: string; title: string; kind: PlanKind; description: string; state: 'existing' | 'planned' | 'unknown'; evidence: PlanEvidence[]; acceptance: string[]; moduleId?: string; category?: string; origin?: 'scan'|'manifest'|'codex'; preview?: PlanPreview }
export interface PlanEdge { from: string; to: string; label: string; evidence: PlanEvidence[]; origin?: 'scan'|'manifest'|'codex'; scopes?: string[] }
export interface PlanDocument { summary: string; kind: string; stack: string[]; nodes: PlanNode[]; edges: PlanEdge[]; questions: string[] }
export interface AnalysisModule { id: string; title: string; path: string; total: number; reviewed: number; status: 'pending'|'partial'|'complete' }
export interface AnalysisFile { path: string; moduleId: string; status: 'pending'|'reviewed'|'stale'; reason?: string }
export interface AnalysisSkill {name:string;version:string;digest:string;adapters:string[];path?:string}
export interface AnalysisContext {owner:"workbench"|"project";cwd:string;targetRoot:string;skill?:AnalysisSkill}
export interface AnalysisGap {id:string;kind:"adapter"|"scanner"|"preview";summary:string;proposal:string;verification:string;evidence:PlanEvidence[];skillVersion:string;runId:string}
export interface ProjectAnalysis { gaps?:AnalysisGap[]; scannedAt: string; fingerprint: string; total: number; reviewed: number; status: 'pending'|'partial'|'complete'; integrated: boolean; modules: AnalysisModule[]; files: AnalysisFile[]; excluded: {reason:string;count:number}[]; warnings: string[]; truncated: boolean }
export interface ProjectRun { context?:AnalysisContext; id: string; role: 'plan' | 'execute'; nodeId: string; title: string; prompt: string; threadId: string; turnId: string; status: 'starting' | 'running' | 'completed' | 'failed' | 'interrupted'; createdAt: string; updatedAt: string; error: string; reply: string; analysis?: 'full'|'module'; moduleId?: string; stage?: string }
export interface PlanState { analysisContext?:AnalysisContext; document: PlanDocument; revision: number; source: 'scan' | 'codex'; updatedAt: string; plannerThreadId: string; runs: ProjectRun[]; analysis?: ProjectAnalysis|null; ui?:ProjectUI; connection: { available: boolean; message: string } }
export interface StartProjectRun { role: 'plan' | 'execute'; prompt: string; nodeId?: string; continueRunId?: string; analysis?: 'full'|'module'; moduleId?: string }
export const planKindLabel: Record<PlanKind,string> = { module:'模块',feature:'功能',page:'页面',api:'接口',component:'组件',storage:'存储',test:'测试',file:'文件' }
