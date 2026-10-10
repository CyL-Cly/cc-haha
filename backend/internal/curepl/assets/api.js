
globalThis.cua = (function createReplApi(bridge) {
  const indicesByApp = new Map, appAliases = new Map, handlePattern = /^g(0|[1-9]\d*):(0|[1-9]\d*)$/u;
  let hasEmittedGuidance = !1;
  const appGuidance = `Native App API
Bind with await cua.getApp("App name, bundle ID, or path"). Keep the returned app between cells.
Observe: app.getAXState({disableDiffing?,emit?}), app.getScreenshot({emit?}), app.getAXStateAndScreenshot({disableDiffing?,emit?}). emit:false returns data without displaying it.
Act: app.click([x,y] or element,{mouseButton?,clickCount?}), app.drag([x,y],[x,y]), app.pressKey(key), app.scroll([x,y] or element,direction,pages?), app.paste(text,{format?}), app.typeText(text), app.selectText(element,text,{prefix?,suffix?,selectionType?}), app.setValue(element,value), app.performSecondaryAction(element,action).
The raw macOS window API is also available as cua.computer (target:"mac"): list_apps(), get_app_state({app,disableDiff?}), and the corresponding snake_case action methods with an explicit app. Raw methods return data without displaying it.
Use observed gN:id handles. Integer indices require a current AX observation; after image-only capture use getAXState({disableDiffing:true}) before using integers. Coordinates refer to the returned screenshot.
Timers: setTimeout/clearTimeout and setInterval/clearInterval use numeric handles and are cleared when the cell ends. If a delay is required, await new Promise(resolve => setTimeout(resolve, milliseconds)); observations normally need no added sleep. Do not schedule actions after the cell returns.
Await actions in loops, then observe at the next decision point. nodeRepl.write(value) emits text; nodeRepl.emitImage(bytes) emits an image. Browser/DOM, imports, Node, filesystem, and networking APIs are unavailable.`;
  function textOf(result) {
    return result.content.filter((block) => block.type === "text" && typeof block.text === "string").map((block) => block.text).join(`
`);
  }
  async function invoke(method, args) {
    const result = await bridge.invoke(method, args);
    if (result.isError) {
      const metadata = result.nativeError, message = metadata?.message ?? (textOf(result) || `Computer Use ${method} failed`), error = metadata?.name === "TypeError" ? TypeError(message) : Error(message);
      if (metadata?.name === "SkyComputerUseError") {
        error.name = metadata.name;
        if (Number.isInteger(metadata.code))
          Reflect.set(error, "code", metadata.code);
        if (typeof metadata.errorName === "string")
          Reflect.set(error, "errorName", metadata.errorName);
        if (metadata.request === null)
          Reflect.set(error, "request", null);
        if (metadata.requestType === "jsonRPC")
          Reflect.set(error, "requestType", metadata.requestType);
      }
      if (typeof metadata?.nativeCode === "string")
        Reflect.set(error, "nativeCode", metadata.nativeCode);
      throw error;
    }
    return result;
  }
  function imageOf(result) {
    const block = result.content.find((item) => item.type === "image");
    return block && typeof block.data === "string" && typeof block.mimeType === "string" ? { data: block.data, mimeType: block.mimeType } : void 0;
  }
  function decodeImage(image) {
    const base64 = image.data;
    if (!/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/u.test(base64))
      throw Error("The screenshot contains invalid base64 data");
    const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/", padding = base64.endsWith("==") ? 2 : base64.endsWith("=") ? 1 : 0, bytes = new Uint8Array(base64.length / 4 * 3 - padding);
    let offset = 0;
    for (let i = 0;i < base64.length; i += 4) {
      const n = alphabet.indexOf(base64[i]) << 18 | alphabet.indexOf(base64[i + 1]) << 12 | Math.max(0, alphabet.indexOf(base64[i + 2])) << 6 | Math.max(0, alphabet.indexOf(base64[i + 3]));
      if (offset < bytes.length)
        bytes[offset++] = n >> 16 & 255;
      if (offset < bytes.length)
        bytes[offset++] = n >> 8 & 255;
      if (offset < bytes.length)
        bytes[offset++] = n & 255;
    }
    return bytes;
  }
  function clearIndices(indices) {
    indices.generation = void 0;
    indices.handles.clear();
  }
  function rememberState(indices, text) {
    const start = text.indexOf(`<app_state>
`), end = text.lastIndexOf(`
</app_state>`), lines = (start >= 0 && end > start ? text.slice(start + 12, end) : text).split(`
`);
    if (!lines.some((line) => line.startsWith("The following is a diff from the previous accessibility tree ") || line.startsWith("There has been no change in the accessibility tree ")))
      clearIndices(indices);
    for (const line of lines) {
      const match = /^[+~]?[\t ]*(g(0|[1-9]\d*):(0|[1-9]\d*))(?=\s|$)/u.exec(line);
      if (!match)
        continue;
      const index = Number(match[3]);
      if (!Number.isSafeInteger(index))
        continue;
      if (indices.generation !== match[2]) {
        clearIndices(indices);
        indices.generation = match[2];
      }
      indices.handles.set(index, match[1]);
    }
    for (const line of lines) {
      if (!line.startsWith("Removed element IDs: "))
        continue;
      for (const range of line.slice(21).split(", ")) {
        const match = /^(\d+)(?:-(\d+))?$/u.exec(range);
        if (!match)
          continue;
        const low = Number(match[1]), high = Number(match[2] ?? match[1]);
        for (const id of indices.handles.keys())
          if (id >= low && id <= high)
            indices.handles.delete(id);
      }
    }
  }
  function element(indices, target) {
    if (typeof target === "string" && handlePattern.test(target))
      return target;
    if (typeof target === "number" && Number.isSafeInteger(target) && target >= 0) {
      const handle = indices.handles.get(target);
      if (handle)
        return handle;
      throw Error(`Element ${target} has no current observed handle. Call getAXState({disableDiffing:true}) before using an integer index.`);
    }
    throw Error("Expected an observed integer element index or an opaque gN:id handle");
  }
  function point(value, name = "coordinate") {
    if (!Array.isArray(value) || !Number.isFinite(value[0]) || !Number.isFinite(value[1]))
      throw TypeError(`${name} must include finite x and y coordinates`);
    return [value[0], value[1]];
  }
  function targetArgs(indices, target) {
    if (Array.isArray(target)) {
      const [x, y] = point(target);
      return { x, y };
    }
    return { element_index: element(indices, target) };
  }
  function mouseButton(value) {
    if (value === void 0)
      return;
    const normalized = typeof value === "string" ? value.trim().toLowerCase() : value;
    if (normalized === 0 || normalized === "left" || normalized === "l")
      return "left";
    if (normalized === 1 || normalized === "right" || normalized === "r")
      return "right";
    if (normalized === 2 || normalized === "middle" || normalized === "m")
      return "middle";
    throw TypeError(typeof value === "number" ? "mouseButton number must be 0, 1, or 2" : "mouseButton must be left, right, middle, l, r, m, 0, 1, or 2");
  }
  function scrollDirection(value) {
    const direction = value.trim().toLowerCase();
    if (direction === "u" || direction === "up")
      return "up";
    if (direction === "d" || direction === "down")
      return "down";
    if (direction === "l" || direction === "left")
      return "left";
    if (direction === "r" || direction === "right")
      return "right";
    throw TypeError("direction must be up, down, left, or right");
  }
  function validatePages(pages) {
    if (pages !== void 0 && (!Number.isFinite(pages) || pages <= 0))
      throw TypeError("pages must be a finite number > 0");
  }
  function indicesFor(selector) {
    const app = appAliases.get(selector) ?? selector;
    let indices = indicesByApp.get(app);
    if (!indices) {
      indices = { handles: new Map };
      indicesByApp.set(app, indices);
    }
    return indices;
  }
  async function emitText(text, options) {
    if (options?.emit !== !1)
      await bridge.emit("text", text);
  }
  async function emitImage(image, options) {
    if (options?.emit !== !1)
      await bridge.emit("image", image);
  }
  async function emitSelectionText(text, options) {
    if (options?.emit === !1)
      return;
    const prefix = hasEmittedGuidance ? "" : `${appGuidance}

`;
    hasEmittedGuidance = !0;
    await emitText(prefix + text);
  }
  async function listApps(options) {
    const result = await invoke("list_apps", {}), text = textOf(result), apps = result.apps ?? text.split(`
`).flatMap((line) => {
      const separator = line.lastIndexOf(" \u2014 ");
      if (separator < 0)
        return [];
      const id = line.slice(separator + 3);
      if (!id)
        return [];
      return [{ id, displayName: line.slice(0, separator), isRunning: !0 }];
    });
    await emitText(JSON.stringify(apps), options);
    return apps;
  }
  async function getState(options) {
    const state = { apps: await listApps({ emit: !1 }), browsers: [] };
    await emitSelectionText(JSON.stringify(state), options);
    return state;
  }
  async function getApp(selector) {
    if (typeof selector !== "string" || !selector.trim())
      throw Error("getApp requires an explicit app name, bundle ID, or path");
    const first = await invoke("get_app_state", { app: selector, disableDiff: !0 }), app = typeof first.app === "string" && first.app ? first.app : selector;
    appAliases.set(selector, app);
    const observed = indicesFor(app), firstText = textOf(first);
    rememberState(observed, firstText);
    await emitSelectionText(firstText);
    async function observe(options) {
      try {
        return await invoke("get_app_state", {
          app,
          ...options?.disableDiffing === void 0 ? {} : { disableDiff: options.disableDiffing }
        });
      } catch (error) {
        clearIndices(observed);
        throw error;
      }
    }
    async function action(method, args) {
      await invoke(method, { app, ...args });
    }
    return {
      async getAXState(options) {
        const text = textOf(await observe(options));
        rememberState(observed, text);
        await emitText(text, options);
        return text;
      },
      async getScreenshot(options) {
        clearIndices(observed);
        const image = imageOf(await observe());
        if (!image)
          throw Error(`Screenshot unavailable for ${app}.`);
        const bytes = decodeImage(image);
        await emitImage(image, options);
        return bytes;
      },
      async getAXStateAndScreenshot(options) {
        const result = await observe(options), state = textOf(result);
        rememberState(observed, state);
        await emitText(state, options);
        const image = imageOf(result);
        if (!image)
          return { state };
        const screenshot = decodeImage(image);
        await emitImage(image, options);
        return { state, screenshot };
      },
      async click(target, options) {
        const button = mouseButton(options?.mouseButton);
        await action("click", {
          ...targetArgs(observed, target),
          ...button === void 0 ? {} : { mouse_button: button },
          ...options?.clickCount === void 0 ? {} : { click_count: options.clickCount }
        });
      },
      async drag(from, to) {
        const [from_x, from_y] = point(from, "from"), [to_x, to_y] = point(to, "to");
        await action("drag", { from_x, from_y, to_x, to_y });
      },
      async pressKey(key) {
        if (key.trim() === "")
          throw TypeError("key is required");
        await action("press_key", { key });
      },
      async scroll(target, direction, pages) {
        validatePages(pages);
        await action("scroll", {
          ...targetArgs(observed, target),
          direction: scrollDirection(direction),
          ...pages === void 0 ? {} : { pages }
        });
      },
      async paste(text, options) {
        await action("paste", { text, format: options?.format ?? "text" });
      },
      async typeText(text) {
        await action("type_text", { text });
      },
      async selectText(index, text, options) {
        await action("select_text", {
          element_index: element(observed, index),
          text,
          ...options?.prefix === void 0 ? {} : { prefix: options.prefix },
          ...options?.suffix === void 0 ? {} : { suffix: options.suffix },
          ...options?.selectionType === void 0 ? {} : { selection_type: options.selectionType }
        });
      },
      async setValue(index, value) {
        await action("set_value", { element_index: element(observed, index), value });
      },
      async performSecondaryAction(index, actionName) {
        await action("perform_secondary_action", { element_index: element(observed, index), action: actionName });
      }
    };
  }
  async function nativeAction(method, input) {
    if (typeof input.app !== "string" || input.app.trim() === "")
      throw TypeError("app is required");
    const args = { ...input };
    if (args.element_index !== void 0)
      args.element_index = element(indicesFor(input.app), args.element_index);
    await invoke(method, args);
  }
  function nativePoint(input) {
    return input.element_index !== void 0 ? { element_index: input.element_index } : { x: point([input.x, input.y])[0], y: input.y };
  }
  return { getApp, listApps, getState, computer: {
    target: "mac",
    list_apps: () => listApps({ emit: !1 }),
    async get_app_state(input) {
      if (typeof input.app !== "string" || input.app.trim() === "")
        throw TypeError("app is required");
      const result = await invoke("get_app_state", {
        app: input.app,
        ...input.disableDiff === void 0 ? {} : { disableDiff: input.disableDiff }
      }).catch((error) => {
        clearIndices(indicesFor(input.app));
        throw error;
      }), app = typeof result.app === "string" && result.app ? result.app : input.app;
      appAliases.set(input.app, app);
      const text = textOf(result);
      rememberState(indicesFor(app), text);
      const image = imageOf(result);
      return { app, text, screenshot: image ? { url: `data:${image.mimeType};base64,${image.data}` } : null };
    },
    click: async (input) => nativeAction("click", {
      app: input.app,
      ...nativePoint(input),
      ...input.mouse_button === void 0 ? {} : { mouse_button: mouseButton(input.mouse_button) },
      ...input.click_count === void 0 ? {} : { click_count: input.click_count }
    }),
    async drag(input) {
      point([input.from_x, input.from_y], "from");
      point([input.to_x, input.to_y], "to");
      return nativeAction("drag", { app: input.app, from_x: input.from_x, from_y: input.from_y, to_x: input.to_x, to_y: input.to_y });
    },
    paste: async (input) => nativeAction("paste", { app: input.app, text: input.text, format: input.format }),
    perform_secondary_action: async (input) => nativeAction("perform_secondary_action", { app: input.app, element_index: input.element_index, action: input.action }),
    async press_key(input) {
      if (input.key.trim() === "")
        throw TypeError("key is required");
      await nativeAction("press_key", { app: input.app, key: input.key });
    },
    async scroll(input) {
      validatePages(input.pages);
      await nativeAction("scroll", {
        app: input.app,
        ...nativePoint(input),
        direction: scrollDirection(input.direction),
        ...input.pages === void 0 ? {} : { pages: input.pages }
      });
    },
    select_text: async (input) => nativeAction("select_text", {
      app: input.app,
      element_index: input.element_index,
      text: input.text,
      ...input.prefix === void 0 ? {} : { prefix: input.prefix },
      ...input.suffix === void 0 ? {} : { suffix: input.suffix },
      ...input.selection_type === void 0 ? {} : { selection_type: input.selection_type }
    }),
    set_value: async (input) => nativeAction("set_value", { app: input.app, element_index: input.element_index, value: input.value }),
    type_text: async (input) => nativeAction("type_text", { app: input.app, text: input.text })
  } };
})({
  invoke: __cuInvoke,
  emit(type, value) {
    __cuEmit(type === 'text' ? { type: 'text', text: value } : { type: 'image', ...value })
  }
});
globalThis.nodeRepl = (function createReplOutput(emit) {
  return {
    async write(value) {
      emit({ type: "text", text: typeof value === "string" ? value : JSON.stringify(value) ?? String(value) });
    },
    async emitImage(value) {
      if (value && typeof value === "object" && "data" in value && typeof value.data === "string") {
        emit({ type: "image", data: value.data, mimeType: value.mimeType });
        return;
      }
      const bytes = value instanceof Uint8Array ? value : value && typeof value === "object" && ("bytes" in value) ? value.bytes : void 0;
      if (!(bytes instanceof Uint8Array))
        throw Error("emitImage requires Uint8Array bytes or {bytes, mimeType}");
      const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/", parts = [];
      for (let i = 0;i < bytes.length; i += 3) {
        const a = bytes[i], b = bytes[i + 1] ?? 0, c = bytes[i + 2] ?? 0;
        parts.push(alphabet[a >> 2] + alphabet[(a & 3) << 4 | b >> 4] + (i + 1 < bytes.length ? alphabet[(b & 15) << 2 | c >> 6] : "=") + (i + 2 < bytes.length ? alphabet[c & 63] : "="));
      }
      emit({
        type: "image",
        data: parts.join(""),
        mimeType: "mimeType" in value && value.mimeType ? value.mimeType : bytes[0] === 255 && bytes[1] === 216 && bytes[2] === 255 ? "image/jpeg" : "image/png"
      });
    }
  };
})(__cuEmit);
