import type { PlanDocument, PlanNode } from './projectPlan'

// Following outgoing links preserves a page's own chain: a shared store never
// pulls in another page through that page's incoming links.
export function projectTopology(document:PlanDocument,selectedId:string,chain:boolean,moduleId='',offset=0,limit=48){
 const all=new Map(document.nodes.map(n=>[n.id,n])),allowed=new Set<string>(),allowedEdges=new Set<number>()
 if(chain&&all.has(selectedId)){
  const outgoing=new Map<string,{index:number;to:string;scopes:string[]}[]>()
  document.edges.forEach((e,index)=>{const list=outgoing.get(e.from)??[];list.push({index,to:e.to,scopes:e.scopes??[]});outgoing.set(e.from,list)})
  const queue=[{id:selectedId,scope:''}],visited=new Set<string>()
  for(let i=0;i<queue.length;i++){
   const current=queue[i]!,key=current.id+'\0'+current.scope
   if(visited.has(key))continue;visited.add(key);allowed.add(current.id)
   for(const edge of outgoing.get(current.id)??[]){
    if(!all.has(edge.to))continue
    let scopes:string[]
    if(!edge.scopes.length)scopes=[current.scope]
    else if(edge.scopes.includes(current.id))scopes=[current.id]
    else if(edge.scopes.includes(current.scope))scopes=[current.scope]
    else if(current.id===selectedId&&!current.scope)scopes=edge.scopes
    else continue
    allowedEdges.add(edge.index)
    for(const scope of scopes)queue.push({id:edge.to,scope})
   }
  }
 }else{
  for(const n of document.nodes)if(!moduleId||n.moduleId===moduleId||n.id===moduleId)allowed.add(n.id)
  document.edges.forEach((e,index)=>{if(allowed.has(e.from)&&allowed.has(e.to))allowedEdges.add(index)})
 }
 const candidates=[...allowed].map(id=>all.get(id)!).filter(Boolean)
 const nodes=candidates.slice(offset,offset+limit),shown=new Set(nodes.map(n=>n.id))
 const edges=document.edges.flatMap((edge,index)=>allowedEdges.has(index)&&shown.has(edge.from)&&shown.has(edge.to)?[{...edge,index}]:[])
 const totalEdges=allowedEdges.size
 return {nodes,edges,total:candidates.length,totalEdges,hiddenEdges:totalEdges-edges.length}
}

export function topologyLayout(nodes:PlanNode[],edges:{from:string;to:string}[]){
 const ids=new Set(nodes.map(n=>n.id)),rank=new Map<string,number>(),degree=new Map(nodes.map(n=>[n.id,0])),outgoing=new Map<string,string[]>()
 for(const e of edges)if(ids.has(e.from)&&ids.has(e.to)&&e.from!==e.to){degree.set(e.to,degree.get(e.to)!+1);const next=outgoing.get(e.from)??[];next.push(e.to);outgoing.set(e.from,next)}
 const queue=nodes.filter(n=>degree.get(n.id)===0).map(n=>n.id)
 for(const id of queue)rank.set(id,0)
 for(let i=0;i<queue.length;i++)for(const next of outgoing.get(queue[i]!)??[]){rank.set(next,Math.max(rank.get(next)??0,(rank.get(queue[i]!)??0)+1));degree.set(next,degree.get(next)!-1);if(degree.get(next)===0)queue.push(next)}
 // Cycles stay visible in a separate bounded set of columns; their reverse edges
 // remain explicit instead of disappearing or causing a recursive layout loop.
 let cycleRank=Math.max(0,...rank.values())+1
 for(const n of nodes)if(degree.get(n.id)!>0&&!queue.includes(n.id))rank.set(n.id,cycleRank++)
 const groups=new Map<number,PlanNode[]>()
 for(const n of nodes){const r=rank.get(n.id)??0;const group=groups.get(r)??[];group.push(n);groups.set(r,group)}
 const positions=new Map<string,{left:number;top:number;width:number;height:number}>();let column=0,maxHeight=260
 for(const [,group]of [...groups.entries()].sort(([a],[b])=>a-b)){
  for(let start=0;start<group.length;start+=8){let top=28
   for(const n of group.slice(start,start+8)){const height=n.preview?.captures.length?232:150;positions.set(n.id,{left:24+column*315,top,width:224,height});top+=height+35}
   maxHeight=Math.max(maxHeight,top+28);column++
  }
 }
 return {positions,width:Math.max(280,column*315-40),height:maxHeight}
}
