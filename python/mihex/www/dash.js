var _ws = null
var _retry = 0
var _loaded = false
var _calling = null

function esc(s) {
	if (s == null) return ''
	return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;').replace(/'/g, '&#39;')
}

function load() {
	var cwidth = document.documentElement.clientWidth
	var count = Math.floor(cwidth / 124)
	var width = (cwidth - count * 4) / count
	width = Math.floor(width * 10) / 10
	try {
		document.styleSheets[1].cssRules[0].style.width = width + 'px'
		document.styleSheets[1].cssRules[1].style.width = Math.ceil(width * 1.5 + 1) + 'px'
	} catch (err) {
		console.log(err.message)
	}

	connect()
}

function connect(reason) {
	_loaded = false
	_calling = null
	floater(reason, 'loading')

	_ws = new WebSocket((location.protocol == 'https:' ? 'wss://' : 'ws://') + location.host + '/websocket' + location.search)

	_ws.onopen = () => _ws.send('{"call": "states"}')
	_ws.onmessage = onMessage
	_ws.onclose = onClose
}

function reconn(reason, delay) {
	if (_ws) {
		console.log('关闭旧连接')
		_ws.onclose = null
		if (_ws.readyState == WebSocket.OPEN)
			_ws.close()
		delete _ws
		_ws = null
	}
	setTimeout(() => connect(reason || '重新连接'), delay * 1000)
}

function onClose(envnt) {
	var seconds = Math.min(Math.pow(4, ++_retry), 3600)
	var delay = (seconds > 60) ? (Math.ceil(seconds / 60) + ' 分钟') : (seconds + ' 秒')
	var text = '连接关闭，' + delay + '后'
	reconn('第' + _retry + '次重连', seconds)
	if (_retry > 1) {
		floater(text)
	} else {
		console.log(text + '重新连接')
	}
}

function onMessage(event) {
	try {
		var data = JSON.parse(event.data)
	} catch (err) {
		if (_calling) endTuning()
		return floater(event.data)
	}

	if (!Array.isArray(data)) {
		return _calling in data ? setTimeout(endTuning, 1000) : renewGrid(data)
	}

	if (!location.hash)
		data.sort(compare)

	if (_loaded) {
		for (var i in data)
			renewGrid(data[i])
	} else {
		var title = data[data.length - 1]?.TITL
		if (title) document.title = title
		reloadAll(data)
	}
}


function reloadAll(data) {
	floater()
	_retry = 0
	_loaded = true
	var htm = ''
	for (var i in data)
		htm += makeGrid(data[i])
	document.getElementById('content').innerHTML = htm
}

function endTuning() {
	var el = document.getElementById(_calling);
	_calling = null
	var cn = el.className;
	if (cn.endsWith(' tuning'))
		el.className = cn.slice(0, -7);
}

// var _refresher = null
function renewGrid(ent) {
	var tile = document.getElementById(ent.NAME)
	if (tile) {
		tile.innerHTML = makeTile(ent)
	} else {
		document.getElementById('content').insertAdjacentHTML('beforebegin', makeGrid(ent))
		// if (_refresher) {
		// 	console.log('忽略新增：' + ent.NAME)
		// } else {
		// 	_refresher = setTimeout("_refresher = null; reconn('新增重连')", 1000 * 60)
		// 	floater('新增“' + ent.NAME + '”，1 分钟后')
		// }
	}
}

var _ICONS = {
	weather: null,
	sensor: null,
	person: 'account',
	binary: 'bullseye',
	device: 'cellphone',
	camera: 'cctv',

	light: 'ceiling-light',
	switch: 'light-switch',
	cover: 'window-closed',
	vacuum: 'robot-vacuum',
	washer: 'washing-machine',
	player: 'play-circle-outline',

	fan: 'fan',
	purifier: null,
	climate: null,
	thermostat: null,
}
var _SORT_TYPES = Object.keys(_ICONS)

function makeGrid(ent) {
	var htm = '<div class="grid ' + esc(ent.TYPE) + '" id="' + esc(ent.NAME) + '"'

	if (ent.LINK)
		htm += " onclick='" + makeLink(ent.LINK) + "'"
	else if (_SORT_TYPES.indexOf(ent.TYPE) >= 5)
		htm += " onclick='onTurn(this)'"
	if (ent.BGND)
		htm += ' style="background-image:url(' + esc(ent.BGND) + ')"'

	return htm + '>' + makeTile(ent) + '</div> '
}


function makeTile(ent) {
	var type = ent.TYPE
	var stat = ent.STAT
	var icon = ent.ICON || _ICONS[type]
	var name = type == 'link' ? '' : ent.NAME
	var ison = ('ISON' in ent) ? (ent.ISON && !['off', 'OFF', '关闭'].includes(ent.ISON)) : (type == 'link' || stat && !['off', 'OFF', '关闭'].includes(stat))
	var soff = ison ? '' : ' off'

	var htm = '<div class="' + (name ? '' : 'no') + 'name ">' + esc(name) + '</div>'
	htm += '<div class="stat' + soff + overflow(ent) + '">'

	if (icon) {
		htm += '<i class="mdi mdi-' + esc(icon) + '"></i>'
	}
	else {
		htm += (stat === false) ? '关' : esc(stat ?? '无')
		if ('UNIT' in ent && stat)
			htm += '<span class="unit">' + esc(ent.UNIT) + '</span>'
	}
	htm += '</div>'

	if (ison) {
		var desc = type == 'link' ? ent.NAME : makeMode(ent)
		if (!desc) {
			if (desc = ent.DESC) {
				if (desc in ent)
					desc = ent[desc]
				desc = esc(desc)
				if (desc.length > 8)
					desc = '<marquee scrollamount="3">' + desc + '</marquee>'
				if ('EXEC' in ent)
					desc = '<span class="moder" onclick=\'event.stopPropagation(); ' + makeLink(ent.EXEC) + "'>" + desc + '</span>'
			} else {
				return htm
			}
		}
		htm += '<div class="desc' + soff + '">' + desc + '</div>'
	}

	return htm
}

function makeLink(link) {
	if (link.charAt(0) == '/' && link.indexOf('?') < 0)
		link += location.search
	return link.startsWith('/') || link.substring(0, 50).includes('://') ? 'location="' + esc(link) + '"' : link
}

function makeMode(ent) {
	var htm = ''
	if ('TOGL' in ent || 'TUNE' in ent)
		htm += '<span class="tuner" onclick="onTune(event)">' + ('TOGL' in ent ? (ent.TOGL == 'on' || ent.TOGL == ent.TOGL_ON ? '◉' : '◌') : '▽') + '</span>'
	if (ent.MODS || 'TUNE' in ent) {
		htm += '<select class="moder" onclick="event.stopPropagation()"'
		if (ent.MODS) {
			htm += ' onchange="onMode(this)">'
			for (var k in ent.MODS) {
				var v = ent.MODS[k]
				if (Array.isArray(ent.MODS))
					k = v
				var selected = (k == ent.MODE) ? ' selected' : ''
				htm += '<option value="' + esc(k) + '"' + selected + '>' + esc(v)
				if (selected && 'TUNE' in ent) {
					htm += ' ' + ent.TUNE
				}
				htm += '</option>'
			}
		} else {
			htm += '><option>' + ent.TUNE + '</option>'
		}
		htm += '</select>'
	}
	if ('TOGR' in ent || 'TUNE' in ent)
		htm += '<span class="tuner" onclick="onTune(event)">' + ('TOGR' in ent ? (ent.TOGR == 'on' || ent.TOGR == ent.TOGR_ON ? '◑' : '◐') : '△') + '</span>'
	return htm
}

function onTurn(grid) {
	var el = grid.children[1]
	var off = el.className.startsWith('stat off')
	call(grid, 'on_turn', off ? 1 : 0)
}

function onTune(event) {
	event.stopPropagation()

	var tuner = event.target
	var extra = tuner.parentElement
	var grid = extra.parentElement
	var tune = tuner.innerText
	call(grid, 'on_tune', tune)
}

function onMode(moder) {
	var desc = moder.parentElement
	var grid = desc.parentElement
	var mode = moder.options[moder.selectedIndex].value
	call(grid, 'on_mode', mode)
}

function call(grid, call, args = []) {
	if (_calling) {
		console.log('忽略调用：' + grid.id)
		return
	}
	_calling = grid.id
	grid.className += ' tuning'

	if (!(args instanceof Array))
		args = [args]
	var body = JSON.stringify([grid.id, call, ...args])
	_ws.send(body)
	console.log('调用服务：' + body)
}


var _UNITS = {
	'aqi': [40, 70],
	'co₂': [900, 1600],
	'℃': [30, 38],
	'%': [72, 82],
	'hcho': [0.08, 0.2],
	'lx': [1000, 1500],
}
var _SORT_UNITS = Object.keys(_UNITS)
var _SORT_NAMES = '天爸妈爷奶大小入玄客餐厨过洗走主浴衣次儿书阳'

function overflow(ent) {
	var range = _UNITS[ent.UNIT]
	if (range) {
		if (ent.STAT >= range[1])
			return ' critical'
		else if (ent.STAT >= range[0])
			return ' caution'
	}
	return ''
}


function floater(text, type) {
	if (text && !type) {
		type = 'error" onclick="floater()'
		text += ' <a href="javascript:reconn()">重新连接</a>'
	}
	document.getElementById('floater').innerHTML = type ? '<div id="' + type + '">' + (text || '') + '</div>' : ''
}


function compare(ent1, ent2) {
	function _compare(sorted, item1, item2) {
		var idx1 = sorted.indexOf(item1) >>> 0
		var idx2 = sorted.indexOf(item2) >>> 0
		return idx1 - idx2
	}
	var ret = _compare(_SORT_TYPES, ent1.TYPE, ent2.TYPE)
	if (ret) return ret

	if (ent1.TYPE == 'sensor') {
		var unit1 = ent1.UNIT
		var unit2 = ent2.UNIT
		ret = _compare(_SORT_UNITS, unit1, unit2)
		if (ret) return ret

		ret = unit1 ? unit1.localeCompare(unit2) : unit2 ? unit2.localeCompare(unit1) : 0
		if (ret) return ret
	}

	return _compare(_SORT_NAMES, ent1.NAME[0], ent2.NAME[0])
	// if (ret) return ret
	// return ent1.NAME.localeCompare(ent2.NAME)
}