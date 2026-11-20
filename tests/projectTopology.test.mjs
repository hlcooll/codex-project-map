import test from 'node:test'
import assert from 'node:assert/strict'
import {projectTopology,topologyLayout} from '../src/projectTopology.ts'
const node=(id,kind='component')=>({id,title:id,kind,description:'',state:'existing',evidence:[],acceptance:[]})
const edge=(from,to)=>({from,to,label:'call',evidence:[]})
test('page scope follows multiple levels without traversing another page through shared middleware',()=>{
 const document={nodes:[node('page','page'),node('feature'),node('api'),node('auth'),node('store'),node('other','page')],edges:[edge('page','feature'),edge('feature','api'),edge('api','auth'),edge('auth','store'),edge('other','auth')]}
 const graph=projectTopology(document,'page',true)
 assert.deepEqual(graph.nodes.map(n=>n.id),['page','feature','api','auth','store']);assert.equal(graph.edges.length,4)
})
test('shared nodes and cycles are retained once and every node has a non-overlapping position',()=>{
 const doc={nodes:['a','b','c','d'].map(x=>node(x)),edges:[edge('a','b'),edge('b','c'),edge('c','b'),edge('a','c'),edge('c','d')]}
 const graph=projectTopology(doc,'a',true),layout=topologyLayout(graph.nodes,graph.edges)
 assert.equal(graph.nodes.length,4);assert.equal(layout.positions.size,4)
 const positions=[...layout.positions.values()];for(let i=0;i<positions.length;i++)for(let j=i+1;j<positions.length;j++){const a=positions[i],b=positions[j];assert.ok(a.left+a.width<=b.left||b.left+b.width<=a.left||a.top+a.height<=b.top||b.top+b.height<=a.top)}
})
test('pagination reports omitted nodes and edges instead of claiming a truncated graph is complete',()=>{
 const doc={nodes:Array.from({length:61},(_,i)=>node(String(i))),edges:Array.from({length:60},(_,i)=>edge(String(i),String(i+1)))}
 const first=projectTopology(doc,'0',true),second=projectTopology(doc,'0',true,'',48)
 assert.equal(first.total,61);assert.equal(first.nodes.length,48);assert.equal(first.hiddenEdges,13);assert.equal(second.nodes.length,13);assert.equal(new Set([...first.nodes,...second.nodes].map(n=>n.id)).size,61)
})
test('shared middleware keeps each API handler within its originating route scope',()=>{
 const scoped=(from,to,scopes)=>({...edge(from,to),scopes})
 const doc={nodes:['page','api-a','api-b','auth','handler-a','handler-b','sessions'].map(id=>node(id)),edges:[edge('page','api-a'),scoped('api-a','auth',['api-a']),scoped('api-b','auth',['api-b']),scoped('auth','handler-a',['api-a']),scoped('auth','handler-b',['api-b']),scoped('auth','sessions',['api-a','api-b'])]}
 const chain=projectTopology(doc,'page',true)
 assert.deepEqual(chain.nodes.map(n=>n.id),['page','api-a','auth','handler-a','sessions'])
 assert.ok(!chain.edges.some(e=>e.to==='handler-b'))
 const shared=projectTopology(doc,'auth',true)
 assert.ok(shared.nodes.some(n=>n.id==='handler-a')&&shared.nodes.some(n=>n.id==='handler-b'))
})
