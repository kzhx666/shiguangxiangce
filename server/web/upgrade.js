// Shared responsive tools: search, family administration and non-destructive smart review.
(()=>{
 const el=(tag,text,parent)=>{const n=document.createElement(tag);if(text)n.textContent=text;if(parent)parent.append(n);return n;};
 const action=(p,text,fn)=>{const b=el('button',text,p);b.type='button';b.onclick=async()=>{try{await fn();}catch(e){toast('操作失败：'+e.message);}};return b;};
 const post=(url,data)=>api(url,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(data)});
 const sheet=title=>{const d=el('dialog','',document.body);d.className='tool-sheet';const head=el('header','',d);el('h2',title,head);action(head,'关闭',()=>d.close());d.addEventListener('close',()=>d.remove());d.showModal();return d;};
 const field=(p,label,type='text',value='')=>{const l=el('label',label,p);const i=el('input','',l);i.type=type;i.value=value;return i;};
 const bar=el('section');bar.className='discovery';bar.setAttribute('aria-label','相册搜索与工具');document.querySelector('#stream').before(bar);
 const form=el('form','',bar);const q=field(form,'搜索照片','search');q.placeholder='文件名、人物、相机或城市附近';
 const advanced=el('details','',form);advanced.open=innerWidth>900;el('summary','日期、类型与常用筛选',advanced);const extra=el('div','',advanced);
 const from=field(extra,'起始日期','date'),to=field(extra,'结束日期','date');
 const kind=el('select','',extra);kind.setAttribute('aria-label','媒体类型');for(const [v,t] of [['all','全部类型'],['photo','照片'],['video','视频']]){const o=el('option',t,kind);o.value=v;}
 const fav=field(extra,'只看收藏','checkbox');
 const search=()=>{if(from.value&&to.value&&from.value>to.value){toast('起始日期不能晚于结束日期');return;}state.search={q:q.value.trim(),from:from.value,to:to.value,fav:fav.checked?'1':''};state.kind=kind.value;reset();};
 action(form,'搜索',search);form.onsubmit=e=>{e.preventDefault();search();};
 action(form,'清除',()=>{q.value=from.value=to.value='';fav.checked=false;kind.value='all';search();});
 action(extra,'保存筛选',()=>{search();localStorage.setItem('lumina_filter_'+localStorage.getItem('lumina_user'),JSON.stringify({...state.search,kind:kind.value}));toast('已保存当前筛选');});
 action(extra,'常用筛选',()=>{const f=JSON.parse(localStorage.getItem('lumina_filter_'+localStorage.getItem('lumina_user'))||'{}');q.value=f.q||'';from.value=f.from||'';to.value=f.to||'';fav.checked=f.fav==='1';kind.value=f.kind||'all';search();});
 const tools=el('div','',bar);tools.className='tool-actions';
 action(tools,'上传任务',()=>openMobileUpload());action(tools,'人物纠错',people);action(tools,'相似照片对比',duplicates);
 action(tools,'家庭成员',members);
 action(tools,'紧凑 / 舒展',()=>{const compact=document.body.classList.toggle('compact-gallery');document.documentElement.style.setProperty('--rowh',compact?'150':'220');localStorage.setItem('lumina_compact',compact?'1':'0');renderAll();});
 const feedback=el('div','',bar);feedback.className='search-feedback';feedback.setAttribute('aria-live','polite');
 setInterval(()=>{if(document.hidden)return;const text=state.loading?'正在加载照片…':state.items.length?'已显示 '+state.items.length+' 项'+(state.done?' · 全部结果':' · 下滑加载更多'):state.search?.q?'没有匹配的照片，试试其他关键词或日期':'上传照片，开始记录家庭时光';if(feedback.textContent!==text)feedback.textContent=text;},700);
 async function members(){
  const me=await api('/api/me');if(!me.admin){toast('家庭成员由管理员管理');return;}
  const d=sheet('家庭成员');el('p','为成员分配独立空间。取消家庭共享后，该成员只可访问自己的空间。',d);
  const list=el('div','',d);const data=await api('/api/members');
  const editor=m=>{const ed=sheet(m?'编辑成员':'添加成员');const name=field(ed,'账号（小写字母开头）','text',m?.name||'');name.disabled=!!m;
   const label=field(ed,'空间名称','text',m?.label||'');const password=field(ed,m?'新密码（留空保留）':'密码（至少 8 位）','password');
   const quota=field(ed,'容量上限 GB（0 表示不限）','number',String((m?.quota||0)/1073741824));quota.min='0';quota.step='0.1';
   const shared=field(ed,'允许访问家庭共享','checkbox');shared.checked=m?m.shared:true;
   action(ed,'保存',async()=>{if(!Number.isFinite(Number(quota.value))||Number(quota.value)<0)throw new Error('容量必须为非负数');await post('/api/members',{name:name.value.trim(),label:label.value.trim(),password:password.value,quota:Math.round(Number(quota.value)*1073741824),shared:shared.checked});ed.close();d.close();await initSpaces();await members();});};
  for(const m of data.members){const row=el('article','',list);el('strong',m.name+' · '+m.label,row);el('p',(m.shared?'可访问家庭共享':'独立空间')+' · '+(m.quota?fmtB(m.quota):'不限容量'),row);if(m.name!=='admin')action(row,'编辑',()=>editor(m));}
  action(d,'添加成员',()=>editor(null));
 }
 async function people(){
  const d=sheet('人物整理');el('p','名称和人工纠错会保留；合并仅限同一空间。',d);
  action(d,'归类新照片',async()=>{await post('/api/people/rebuild',{});d.close();people();});
  const data=await api('/api/people');const list=el('div','',d);list.className='review-grid';
  for(const p of data.people){const card=el('article','',list);const img=el('img','',card);img.src='/f/'+p.cover+'.jpg';img.alt=p.name||'未命名人物';img.loading='lazy';el('h3',p.name||'未命名人物',card);el('p',p.count+' 张人脸',card);
   action(card,'命名',async()=>{const ed=sheet('人物名称');const name=field(ed,'名称','text',p.name);action(ed,'保存',async()=>{await post('/api/people/'+p.id+'/name',{name:name.value});ed.close();d.close();people();});});
   action(card,'合并到…',()=>{const ed=sheet('选择同一人物');for(const t of data.people.filter(t=>t.id!==p.id))action(ed,t.name||'人物 '+t.id,async()=>{await post('/api/people/merge',{from:p.id,to:t.id});ed.close();d.close();people();});});
   action(card,'纠正误分',async()=>{const ed=sheet('选出不属于此人物的人脸');const faces=(await api('/api/people/'+p.id+'/faces')).faces;const ids=new Set();const grid=el('div','',ed);grid.className='review-grid';for(const f of faces){const label=el('label','',grid);const img=el('img','',label);img.src='/f/'+f.id+'.jpg';img.alt='选择人脸';const cb=el('input','',label);cb.type='checkbox';cb.onchange=()=>cb.checked?ids.add(f.id):ids.delete(f.id);}
    const name=field(ed,'新人物名称');action(ed,'移到新人物',async()=>{if(!ids.size)throw new Error('请先选择人脸');await post('/api/people/split',{person:p.id,faces:[...ids],name:name.value});ed.close();d.close();people();});});
  }
  if(!data.people.length)el('p','还没有人物结果。连接电脑识别工具后，新照片可自动加入。',d);
 }
 async function duplicates(){
  const d=sheet('相似照片对比');el('p','优先展示文件较大的版本，仅供参考；请检查画面、清晰度与拍摄时间后再决定。',d);
  const data=await api('/api/dupes');for(const g of data.groups){const section=el('section','',d);el('h3',g.items.length+' 张相似照片',section);const grid=el('div','',section);grid.className='review-grid';const selected=new Set();
   for(let i=0;i<g.items.length;i++){const a=g.items[i],card=el('article','',grid);const link=el('a','',card);link.href='/m/'+a.id;link.target='_blank';link.rel='noopener';const img=el('img','',link);img.src='/t/'+a.id+'/t.jpg';img.loading='lazy';img.alt=a.name;el('p',a.name,card);el('small',fmtB(a.size)+(i===0?' · 优先检查保留':''),card);const cb=field(card,'移入回收站','checkbox');cb.onchange=()=>cb.checked?selected.add(a.id):selected.delete(a.id);}
   action(section,'移入回收站',async()=>{if(!selected.size)throw new Error('请先选择照片');if(selected.size===g.items.length)throw new Error('至少保留一张');if(!confirm('将所选 '+selected.size+' 张照片移入回收站？'))return;await post('/api/trash',{ids:[...selected]});section.remove();reset();});
  }if(!data.groups.length)el('p','没有发现相似照片。',d);
 }
 window.luminaTools={members,people,duplicates};
})();
