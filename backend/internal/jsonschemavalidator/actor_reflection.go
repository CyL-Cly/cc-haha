package jsonschemavalidator

// Schema reflection keeps the caller's getter receiver and object identity while
// giving the generic compiler a host-realm diagnostic for a thrown VM value.
// Proxy targets are private configurable shells: a frozen schema remains legal
// and does not violate Proxy invariants when a nested object is reflected.
const actorBridgeSource = `
const Ajv=globalThis.__WorkflowAjv,parse=JSON.parse,proxies=new WeakMap();
const wrap=value=>{
  if(value===null||typeof value!=='object')return value;
  let proxy=proxies.get(value);
  if(proxy)return proxy;
  const array=Array.isArray(value);
  const shell=array?[]:Object.create(Object.getPrototypeOf(value));
  proxy=new Proxy(shell,{
    get(_target,key){
      let result;
      try{result=Reflect.get(value,key,value)}catch(e){throw new Error(String(e))}
      if(key==='toJSON'&&typeof result==='function')return function(...args){
        try{return Reflect.apply(result,value,args)}catch(e){throw new Error(String(e))}
      };
      return wrap(result);
    },
    has(_target,key){return Reflect.has(value,key)},
    ownKeys(){return Reflect.ownKeys(value)},
    getPrototypeOf(){return Reflect.getPrototypeOf(value)},
    getOwnPropertyDescriptor(_target,key){
      const descriptor=Reflect.getOwnPropertyDescriptor(value,key);
      if(!descriptor)return undefined;
      if(array&&key==='length'){
        Reflect.defineProperty(shell,'length',{value:descriptor.value,writable:descriptor.writable});
        return descriptor;
      }
      const result={...descriptor,configurable:true};
      if('value'in result)result.value=wrap(result.value);
      return result;
    },
    set(_target,key,next){return Reflect.set(value,key,next,value)},
    deleteProperty(_target,key){return Reflect.deleteProperty(value,key)}
  });
  proxies.set(value,proxy);
  return proxy;
};
return {
  parse:json=>parse(json),
  build:source=>{
    try{
      const schema=wrap(source),ajv=new Ajv({allErrors:true});
      if(!ajv.validateSchema(schema))return {error:ajv.errorsText(ajv.errors)};
      return {validate:ajv.compile(schema)};
    }catch(e){return {error:e instanceof Error?e.message:String(e)}}
  },
  errors:validate=>(validate.errors??[]).map(e=>(e.instancePath||'root')+': '+e.message).join(', ')
};
})`
