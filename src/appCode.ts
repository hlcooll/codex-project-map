export interface CodeLocation { id: string; file: string; line: number }
export interface CodeNode extends CodeLocation {
  key?: string
  title: string
  kind: string
  evidence?: CodeLocation[]
  uses?: CodeNode[]
  graph?: { nodes: CodeNode[]; edges: CodeRelation[] }
}
export interface CodeRelation {
  from: string
  to: string
  label: string
  evidence?: CodeLocation[]
}
