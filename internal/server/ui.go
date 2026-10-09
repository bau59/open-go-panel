package server

import (
	"html"
	"strings"
)

const baseStyles = `
	:root{
		color-scheme:dark;
		--bg:#081326;--surface:#142944;--surface-2:#1a3253;--surface-3:#203c60;
		--border:rgba(148,190,246,.20);--border-strong:rgba(163,203,255,.33);
		--text:#edf5ff;--muted:#b0c3da;--muted-2:#91a8c5;
		--primary:#7777ee;--primary-hover:#898bff;--primary-soft:rgba(128,138,255,.16);
		--success:#48dfae;--success-soft:rgba(72,223,174,.13);
		--warning:#f5ca78;--warning-soft:rgba(245,202,120,.13);
		--danger:#ff829a;--danger-soft:rgba(255,130,154,.13);
		--radius:15px;--radius-lg:20px;--shadow:0 24px 56px rgba(2,10,28,.32);
	}
	*{box-sizing:border-box}
	html{background:var(--bg)}
	body{margin:0;min-height:100vh;
		background:radial-gradient(ellipse 55vw 60vh at 100% 0%,rgba(56,164,247,.19),transparent 80%),
			radial-gradient(ellipse 42vw 55vh at 14% 100%,rgba(168,104,236,.12),transparent 80%),
			linear-gradient(155deg,#0b1932,#081326 58%,#0b172c);
		background-attachment:fixed;color:var(--text);
		font-family:Inter,ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;
		-webkit-font-smoothing:antialiased;
	}
	a{color:inherit;text-decoration:none}
	button,input,select,textarea{font:inherit}
	button{cursor:pointer}
	button,.button,.secondary,.danger,.icon-button,.pager-button,.tab,.nav a,summary{
		-webkit-tap-highlight-color:transparent;
	}
	button,.button,.secondary,.danger,.icon-button,.pager-button,.tab{
		appearance:none;
		-webkit-appearance:none;
		outline:0;
		text-decoration:none;
		user-select:none;
	}
	button:focus,summary:focus,a:focus{outline:none}
	button:focus-visible,.button:focus-visible,.secondary:focus-visible,.danger:focus-visible,.icon-button:focus-visible,.pager-button:focus-visible,.tab:focus-visible,.nav a:focus-visible,summary:focus-visible{
		outline:none;
		box-shadow:0 0 0 3px rgba(109,93,252,.22);
	}
	button:disabled,.button[disabled],.secondary[disabled],.danger[disabled],.pager-button.disabled{
		opacity:.45;
		cursor:not-allowed;
		pointer-events:none;
	}

	.shell{width:min(1480px,calc(100% - 322px));min-width:0;margin:0 24px 0 298px}
	.topbar-wrap{position:fixed;inset:0 auto 0 0;z-index:40;width:278px;padding:16px 12px 16px 16px}
	.topbar-wrap .shell{width:100%;height:100%;margin:0}
	.topbar{position:relative;display:flex;flex-direction:column;align-items:stretch;gap:0;height:100%;min-height:0;padding:22px 13px 15px;overflow-y:auto;overscroll-behavior:contain;border:1px solid rgba(164,204,255,.24);border-radius:24px;background:linear-gradient(165deg,rgba(32,58,98,.88),rgba(15,33,63,.92) 48%,rgba(16,29,53,.94));box-shadow:0 22px 64px rgba(2,9,26,.33),inset 0 1px 0 rgba(221,240,255,.12);backdrop-filter:blur(18px)}
	.topbar::before{content:"";position:absolute;top:0;left:34px;right:34px;height:1px;background:linear-gradient(90deg,transparent,#9cecff,transparent);pointer-events:none}
	.brand{display:flex;align-items:center;gap:12px;padding:0 9px;color:var(--text)}
	.brand-mark{display:grid;place-items:center;width:43px;height:43px;flex:0 0 auto;border:1px solid rgba(206,228,255,.4);border-radius:14px;background:linear-gradient(140deg,#b38cf8,#777ded 48%,#5cceea);box-shadow:0 8px 25px rgba(102,162,242,.27),inset 0 1px 0 rgba(255,255,255,.42);font-size:12px;font-weight:850;color:white}
	.brand-copy{display:flex;flex-direction:column;gap:3px;min-width:0}
	.brand-copy strong{font-size:14px;line-height:1.15;letter-spacing:-.025em}
	.brand-copy small{color:#8faacd;font-size:9px;font-weight:750;letter-spacing:.16em}
	.nav{display:flex;flex:0 0 auto;flex-direction:column;align-items:stretch;gap:4px;margin:31px 0 0;padding:0;border:0;border-radius:0;background:transparent}
	.nav a{display:flex;align-items:center;gap:10px;min-height:42px;padding:9px 12px;border:1px solid transparent;border-radius:12px;color:#b3c8e3;font-size:12px;font-weight:650;line-height:1.25;transition:background-color .15s ease,border-color .15s ease,color .15s ease}
	.nav a:hover{border-color:rgba(148,190,246,.13);background:rgba(144,191,255,.10);color:#fff}
	.nav a.active{border-color:rgba(167,189,255,.32);background:linear-gradient(115deg,rgba(136,126,255,.33),rgba(87,194,239,.16));color:#fff;box-shadow:inset 0 1px 0 rgba(225,239,255,.13)}
	.nav-icon{display:grid;place-items:center;width:22px;height:22px;flex:0 0 auto;color:#8ebbe3}
	.nav-icon svg{display:block;width:18px;height:18px;fill:none;stroke:currentColor;stroke-width:1.8;stroke-linecap:round;stroke-linejoin:round}
	.nav a.active .nav-icon{color:#c3e8ff}
	.nav>details{position:relative;min-width:0;margin-top:9px}
	.nav>details>summary{display:flex;align-items:center;justify-content:space-between;min-height:32px;padding:5px 12px;border-radius:9px;color:#8ea8cb;font-size:10px;font-weight:800;letter-spacing:.09em;text-transform:uppercase;white-space:nowrap}
	.nav>details>summary::-webkit-details-marker{display:none}
	.nav>details>summary::after{content:"⌄";margin-left:8px;font-size:14px;transition:transform .15s ease}
	.nav>details[open]>summary::after{transform:rotate(180deg)}
	.nav>details>summary:hover,.nav>details.current>summary{color:#d8e9ff;background:rgba(160,201,255,.065)}
	.nav-dropdown{position:static;display:flex;flex-direction:column;gap:3px;min-width:0;margin:3px 0 0 16px;padding:2px 0 2px 8px;border:0;border-left:1px solid rgba(147,181,235,.22);border-radius:0;background:transparent;box-shadow:none}
	.nav-dropdown a{width:100%;min-width:0;white-space:normal}
	.sidebar-note{display:flex;align-items:center;gap:8px;margin:auto 8px 14px;padding-top:22px;color:#88a7cb;font-size:11px;font-weight:600}
	.sidebar-note-dot{width:7px;height:7px;border-radius:50%;background:var(--success);box-shadow:0 0 0 4px rgba(72,223,174,.11)}
	.header-actions{display:flex;align-items:stretch;flex-direction:column;gap:8px;padding:13px 4px 0;border-top:1px solid rgba(154,190,239,.13)}
	.header-actions form,.header-actions button{width:100%}
	.header-actions .secondary{height:36px;border-color:rgba(165,204,255,.20);background:rgba(105,150,209,.09);color:#c8daef}

	.icon-button,.button,.secondary,.danger{display:inline-flex;align-items:center;justify-content:center;height:40px;padding:0 14px;border-radius:11px;border:1px solid transparent;font-weight:700;font-size:13px;line-height:1;vertical-align:middle;transition:background-color .15s ease,border-color .15s ease,color .15s ease,box-shadow .15s ease,transform .08s ease}
	.button{border-color:rgba(222,233,255,.28);background:linear-gradient(110deg,#927bf6,#747fe9 48%,#42bbdf);color:#fff;box-shadow:0 8px 24px rgba(69,122,209,.19),inset 0 1px 0 rgba(255,255,255,.24)}
	.button:hover{background:linear-gradient(110deg,#a18cfa,#848df7 48%,#55cbe8);border-color:rgba(231,242,255,.53)}
	.secondary,.icon-button{border-color:var(--border-strong);background:rgba(45,77,120,.38);color:#d5e6fb}
	.secondary:hover,.icon-button:hover{border-color:rgba(161,218,255,.56);background:rgba(67,108,153,.46);color:#fff}
	.danger{border-color:rgba(255,107,122,.3);background:var(--danger-soft);color:#ff9aa5}
	.danger:hover{border-color:rgba(255,107,122,.5);background:rgba(255,107,122,.18);color:#ffc0c7}
	.button:active,.secondary:active,.danger:active,.icon-button:active,.pager-button:active,.tab:active{transform:translateY(1px)}
	main{padding:43px 0 80px;min-width:0}
	.page-head{display:flex;align-items:flex-end;justify-content:space-between;gap:20px;margin-bottom:24px}
	.eyebrow{margin:0 0 8px;color:#a2d9ff;font-size:12px;font-weight:800;letter-spacing:.08em;text-transform:uppercase}
	h1{margin:0;font-size:32px;line-height:1.15;letter-spacing:-.035em}
	h2{margin:0;font-size:16px;letter-spacing:-.015em}
	.sub{margin:9px 0 0;color:var(--muted);font-size:14px;line-height:1.55}
	.panel{min-width:0;border:1px solid var(--border);border-radius:var(--radius-lg);background:linear-gradient(145deg,rgba(34,60,101,.74),rgba(18,38,68,.87) 60%,rgba(19,34,64,.88));box-shadow:0 12px 40px rgba(2,9,27,.19),inset 0 1px 0 rgba(221,240,255,.07)}
	.panel-pad{padding:22px}
	.grid{display:grid;gap:16px}
	.cards{grid-template-columns:repeat(auto-fit,minmax(230px,1fr))}
	.card{position:relative;min-width:0;min-height:156px;padding:22px;border:1px solid var(--border);border-radius:var(--radius-lg);background:linear-gradient(145deg,rgba(36,69,114,.66),rgba(18,38,69,.82));box-shadow:inset 0 1px 0 rgba(221,240,255,.06);transition:background-color .15s ease,border-color .15s ease,transform .15s ease}
	a.card:hover{transform:translateY(-2px);border-color:rgba(161,218,255,.52);background:linear-gradient(145deg,rgba(46,84,132,.75),rgba(23,48,86,.89))}
	.card-icon{display:grid;place-items:center;width:38px;height:38px;margin-bottom:24px;border:1px solid rgba(109,93,252,.22);border-radius:12px;background:var(--primary-soft);color:#a89fff;font-size:12px;font-weight:850}
	.card h2{margin-bottom:7px}
	.card p{margin:0;color:var(--muted);font-size:13px;line-height:1.55}
	.toolbar{display:grid;gap:10px;padding:16px;border-bottom:1px solid var(--border)}
	.toolbar-4{grid-template-columns:1fr 1fr 1fr auto}
	.toolbar-3{grid-template-columns:1fr 1fr auto}
	.table-actions{justify-content:flex-start}
	.field,input,select,textarea{width:100%;min-width:0;border:1px solid var(--border-strong);border-radius:11px;background:rgba(8,23,45,.62);color:var(--text);outline:none;transition:border-color .15s ease,box-shadow .15s ease,background-color .15s ease}
	input,select{height:42px;padding:0 12px}
	select{
		appearance:none;
		-webkit-appearance:none;
		padding-right:42px;
		background-color:rgba(8,23,45,.72);
		background-image:url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='14' height='14' viewBox='0 0 24 24' fill='none' stroke='%23909aab' stroke-width='2' stroke-linecap='round' stroke-linejoin='round'%3E%3Cpath d='m6 9 6 6 6-6'/%3E%3C/svg%3E");
		background-repeat:no-repeat;
		background-position:right 14px center;
	}
	select:hover{border-color:rgba(162,201,255,.43)}
	select option{background:#152944;color:var(--text)}
	.check-row{display:flex;align-items:center;gap:9px;color:#c8d0dc;font-size:13px;font-weight:650;cursor:pointer}
	.check-row input[type="checkbox"]{width:17px;height:17px;margin:0;accent-color:var(--primary)}
	.service-grid-3{grid-template-columns:repeat(3,minmax(0,1fr))}
	.service-grid-4{grid-template-columns:repeat(4,minmax(0,1fr))}
	textarea{padding:11px 12px;resize:vertical}
	input:focus,select:focus,textarea:focus{border-color:#89c8ff;box-shadow:0 0 0 3px rgba(103,188,255,.18)}
	input::placeholder,textarea::placeholder{color:#849bb9}
	label{display:block;margin-bottom:7px;color:#b8c0cd;font-size:12px;font-weight:700}
	table{width:100%;border-collapse:collapse}
	th{padding:13px 16px;border-bottom:1px solid var(--border);color:#abc4df;font-size:10px;text-align:left;text-transform:uppercase;letter-spacing:.085em;font-weight:800;background:rgba(17,40,74,.29)}
	td{padding:15px 16px;border-bottom:1px solid rgba(146,184,235,.12);font-size:13px;vertical-align:middle}
	tbody tr:last-child td{border-bottom:0}
	tbody tr:hover{background:rgba(130,189,249,.055)}
	.muted,.note{color:var(--muted-2);font-size:12px}
	.muted{margin-top:4px}
	.badge,.status-badge,.meta-chip{display:inline-flex;align-items:center;justify-content:center;gap:7px;min-height:26px;padding:0 10px;border-radius:999px;border:1px solid var(--border);background:#111824;color:#bac3d1;font-size:11px;font-weight:750;line-height:1;vertical-align:middle;white-space:nowrap;flex:0 0 auto}
	.status-badge::before{content:"";width:6px;height:6px;border-radius:50%;background:#778196}
	.status-badge.ok{border-color:rgba(56,217,150,.2);background:var(--success-soft);color:#8ceabc}
	.status-badge.ok::before{background:var(--success)}
	.status-badge.warn{border-color:rgba(246,196,83,.2);background:var(--warning-soft);color:#f7d884}
	.status-badge.warn::before{background:var(--warning)}
	.status-badge.danger{border-color:rgba(255,107,122,.25);background:var(--danger-soft);color:#ffabb4}
	.status-badge.danger::before{background:var(--danger)}
	.badge.ok{border-color:rgba(56,217,150,.2);background:var(--success-soft);color:#8ceabc}
	.badge.warn{border-color:rgba(246,196,83,.2);background:var(--warning-soft);color:#f7d884}
	.badge.danger{border-color:rgba(255,107,122,.25);background:var(--danger-soft);color:#ffabb4}
	.meta-chip{background:rgba(8,23,45,.5);color:var(--muted);font-weight:700}
	.state-text{display:inline-flex;align-items:center;gap:8px;min-height:26px;color:var(--muted);font-size:12px;font-weight:750;line-height:1;white-space:nowrap}
	.state-text i{display:block;width:7px;height:7px;border-radius:50%;background:#778196;box-shadow:0 0 0 4px rgba(119,129,150,.08)}
	.state-text.ok{color:#8ceabc}
	.state-text.ok i{background:var(--success);box-shadow:0 0 0 4px var(--success-soft)}
	.state-text.warn{color:#f7d884}
	.state-text.warn i{background:var(--warning);box-shadow:0 0 0 4px var(--warning-soft)}
	.state-text.danger{color:#ffabb4}
	.state-text.danger i{background:var(--danger);box-shadow:0 0 0 4px var(--danger-soft)}
	.page-control-cluster{display:flex;align-items:center;justify-content:flex-end;gap:10px;flex-wrap:wrap}
	.page-control-cluster form{margin:0}
	.service-state{display:flex;align-items:center;gap:10px;min-height:40px;padding:0 13px;border:1px solid var(--border);border-radius:11px;background:rgba(8,23,45,.5)}
	.service-state>span:first-child{color:#c8d0dc;font-size:12px;font-weight:700}

	.actions{display:flex;align-items:center;justify-content:flex-end;gap:7px;flex-wrap:wrap}
	.actions form,.section-title form{margin:0}
	.metric form{margin-top:12px}
	.metric .button,.metric .secondary{width:max-content}
	.toolbar .button{height:42px;white-space:nowrap}
	.alert{margin-bottom:16px;padding:12px 14px;border:1px solid rgba(255,107,122,.25);border-radius:12px;background:var(--danger-soft);color:#ffabb4;font-size:13px}
	.empty{padding:38px!important;text-align:center;color:var(--muted-2)}
	code{font-family:"SFMono-Regular",Consolas,monospace;color:#b8c1d1;font-size:12px}
	.meta-grid{display:grid;grid-template-columns:130px 1fr;gap:12px 18px;font-size:13px}
	.meta-grid>span{color:var(--muted-2)}
	.section-title{display:flex;align-items:flex-start;justify-content:space-between;gap:16px;margin-bottom:18px}
	.metrics-grid{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:12px}
	.metric{min-width:0;padding:18px;border:1px solid var(--border);border-radius:16px;background:linear-gradient(135deg,rgba(39,75,123,.65),rgba(21,43,76,.83));box-shadow:inset 0 1px 0 rgba(220,239,255,.07)}
	.metric>span{display:block;color:var(--muted-2);font-size:11px;font-weight:800;text-transform:uppercase;letter-spacing:.07em}
	.metric small .status-badge{display:inline-flex;text-transform:none;letter-spacing:0}
	.metric strong{display:block;margin-top:8px;font-size:18px;letter-spacing:-.02em}
	.metric small{display:block;margin-top:7px;color:var(--muted);font-size:11px}
	.meter{height:6px;margin-top:11px;overflow:hidden;border-radius:999px;background:rgba(6,18,37,.7)}
	.meter i{display:block;height:100%;border-radius:inherit;background:linear-gradient(90deg,#8c85ff,#5fcde8)}
	.tabs{display:flex;gap:6px;margin-bottom:18px}
	.tab{padding:7px 10px;border:1px solid var(--border);border-radius:9px;background:rgba(8,23,45,.5);color:var(--muted);font-size:12px;font-weight:700}
	.codearea{min-height:320px;font-family:"SFMono-Regular",Consolas,monospace;font-size:12px;line-height:1.55}
	.logbox{min-height:420px;max-height:65vh;overflow:auto;margin:0;padding:18px;border:1px solid var(--border);border-radius:14px;background:#071324;color:#c6cfdd;font:12px/1.6 "SFMono-Regular",Consolas,monospace;white-space:pre-wrap}
	.security-output{max-height:420px;overflow:auto;margin:0;padding:16px;border:1px solid var(--border);border-radius:12px;background:#071324;color:#c6cfdd;font:11px/1.55 "SFMono-Regular",Consolas,monospace;white-space:pre;overscroll-behavior:contain}
	.compact-form{display:grid;grid-template-columns:1fr auto;gap:8px;margin-top:12px}
	.compact-form-3{grid-template-columns:1fr 1fr auto}
	.security-grid{grid-template-columns:1fr 1fr}
	.security-component{display:flex;min-height:210px;flex-direction:column}
	.security-component-head{display:flex;align-items:center;justify-content:space-between;gap:10px}
	.security-component-head>span:first-child{color:var(--muted-2);font-size:10px;font-weight:800;text-transform:uppercase;letter-spacing:.07em}
	.security-component>.note{margin:10px 0 0;line-height:1.45}
	.component-actions{display:flex;align-items:center;gap:7px;flex-wrap:wrap;margin-top:auto;padding-top:16px}
	.component-actions form{margin:0}

	.settings-list{display:grid;gap:10px}
	.setting-switch{display:flex;align-items:center;justify-content:space-between;gap:20px;margin:0;padding:14px 0;border-bottom:1px solid var(--border);cursor:pointer}
	.setting-switch:last-child{border-bottom:0}
	.setting-switch strong{display:block;color:var(--text);font-size:14px}
	.setting-switch>div>span{display:block;margin-top:4px;color:var(--muted);font-size:12px;line-height:1.45}
	.switch{position:relative;display:inline-flex!important;width:44px;height:24px;flex:0 0 auto}
	.switch input{position:absolute;opacity:0;pointer-events:none}
	.switch i{position:absolute;inset:0;border:1px solid var(--border-strong);border-radius:999px;background:#0a1d36;transition:.18s ease}
	.switch i::after{content:"";position:absolute;top:3px;left:3px;width:16px;height:16px;border-radius:50%;background:#7d8798;transition:.18s ease}
	.switch input:checked+i{border-color:rgba(109,93,252,.5);background:var(--primary)}
	.switch input:checked+i::after{transform:translateX(20px);background:#fff}
	.switch input:focus-visible+i{box-shadow:0 0 0 3px rgba(109,93,252,.22)}
	.domain-summary{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:10px}
	.domain-summary>div,.app-overview-grid>div{min-width:0;padding:13px 14px;border:1px solid var(--border);border-radius:13px;background:rgba(8,23,45,.5)}
	.domain-summary span,.app-overview-grid span{display:block;margin-bottom:6px;color:var(--muted-2);font-size:10px;font-weight:800;text-transform:uppercase;letter-spacing:.07em}
	.domain-summary strong,.app-overview-grid strong{display:block;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-size:13px}
	.domain-summary code,.app-overview-grid code{display:block;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
	.advanced-block>summary{list-style:none}
	.advanced-block>summary::-webkit-details-marker{display:none}
	.app-overview-grid{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:10px}
	.app-sections{padding-top:6px;padding-bottom:6px}
	.app-section{padding:18px 0}
	.app-section+.app-section{border-top:1px solid var(--border)}
	.health-grid{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:10px}
	.health-grid .metric{padding:14px}
	.health-grid .metric strong{font-size:16px}
	.health-state.ok{color:#8ceabc}
	.health-state.warn{color:#f7d884}
	.health-state.danger{color:#ffabb4}
	.app-form-row{display:grid;gap:8px}
	.app-form-row-deploy{grid-template-columns:minmax(0,1fr) 160px auto}
	.app-promotion-grid{display:grid;grid-template-columns:minmax(0,1.4fr) minmax(0,1fr) auto;gap:12px;align-items:end;margin-top:14px}
	.app-promotion-grid>div{min-width:0}
	.app-promotion-grid select,.app-promotion-grid input{width:100%;min-width:0}
	.app-promotion-grid button{white-space:nowrap}
	.app-form-row-db{grid-template-columns:minmax(0,1fr) 170px auto}
	.service-summary{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:10px}
	.service-summary>div{min-width:0;padding:13px 14px;border:1px solid var(--border);border-radius:13px;background:rgba(8,23,45,.5)}
	.service-summary span{display:block;margin-bottom:6px;color:var(--muted-2);font-size:10px;font-weight:800;text-transform:uppercase;letter-spacing:.07em}
	.service-summary strong{display:block;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-size:13px}
	.service-form-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:12px}
	.service-form-grid .span-2{grid-column:1/-1}
	.service-editor>summary{display:inline-flex}
	.backup-row{display:flex;align-items:center;justify-content:space-between;gap:12px;padding:9px 0;border-bottom:1px solid var(--border)}
	.backup-row:last-child{border-bottom:0}
	.list-toolbar{display:flex;align-items:center;gap:8px;padding:14px 16px;border-bottom:1px solid var(--border)}
	.list-toolbar input{max-width:420px}
	.pager{display:flex;align-items:center;justify-content:space-between;gap:12px;padding:14px 16px;border-top:1px solid var(--border)}
	.pager-info{color:var(--muted);font-size:12px}
	.pager-actions{display:flex;gap:8px}
	.pager-button{display:inline-flex;align-items:center;justify-content:center;height:36px;padding:0 12px;border:1px solid var(--border-strong);border-radius:10px;background:var(--surface);color:#c8d0dc;font-size:12px;font-weight:700;line-height:1;transition:background-color .15s ease,border-color .15s ease,color .15s ease,box-shadow .15s ease,transform .08s ease}
	.pager-button:hover{border-color:rgba(162,201,255,.43);background:var(--surface-2);color:#fff}
	.pager-button.disabled{opacity:.4;pointer-events:none}
	.log-toolbar{display:grid;grid-template-columns:minmax(200px,1fr) 150px 190px 190px auto;gap:8px;margin-bottom:14px}
	.log-line{display:block;padding:2px 0;border-bottom:1px solid rgba(255,255,255,.025)}
	.table-tools{display:flex;align-items:center;justify-content:space-between;gap:10px;padding:12px 16px;border-bottom:1px solid var(--border)}
	.table-tools-search{max-width:360px}
	.table-tools-right{display:flex;align-items:center;gap:8px}
	.table-tools-right select{width:92px;height:36px}
	.app-page-head{display:flex;align-items:flex-end;justify-content:space-between;gap:20px;margin-bottom:22px}
	.app-breadcrumb{color:var(--muted);font-size:12px;font-weight:700}
	.app-breadcrumb:hover{color:var(--text)}
	.app-title-row{display:flex;align-items:center;gap:10px;flex-wrap:wrap}
	.app-runtime-card{margin-bottom:16px}
	.runtime-head{display:flex;align-items:center;justify-content:space-between;gap:20px}
	.runtime-state{display:flex;align-items:center;gap:12px;flex-wrap:wrap}
	.runtime-state>strong{font-size:28px;line-height:1.1;letter-spacing:-.03em}
	.runtime-status-line{display:flex;align-items:center;gap:9px;margin-top:8px}
	.runtime-actions{justify-content:flex-end}
	.runtime-facts{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:10px;margin-top:18px}
	.runtime-facts>div{min-width:0;padding:12px 13px;border:1px solid var(--border);border-radius:12px;background:rgba(8,23,45,.5)}
	.runtime-facts span{display:block;margin-bottom:6px;color:var(--muted-2);font-size:10px;font-weight:800;text-transform:uppercase;letter-spacing:.07em}
	.runtime-facts strong,.runtime-facts code{display:block;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-size:12px}
	.app-dashboard-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:16px}
	.app-card{min-width:0}
	.app-card-wide{grid-column:1/-1}
	.app-card .domain-summary{grid-template-columns:repeat(2,minmax(0,1fr))}
	.attachment-row{padding:12px 13px;margin:8px 0;border:1px solid var(--border);border-radius:12px;background:rgba(8,23,45,.5)}
	.service-settings-card{scroll-margin-top:92px}
	.service-settings-body{margin-top:18px;padding-top:18px;border-top:1px solid var(--border)}
	.danger-zone{border-color:rgba(255,107,122,.18)}
	.danger-zone-body{display:flex;align-items:center;justify-content:space-between;gap:20px;margin-top:18px;padding-top:18px;border-top:1px solid rgba(255,107,122,.18)}
	.caddy-setting-section{margin-top:20px;padding-top:20px;border-top:1px solid var(--border)}
	.caddy-setting-section h3{margin:0 0 6px;font-size:14px;font-weight:800;color:var(--text)}
	.caddy-setting-section>.note{margin:0 0 12px;line-height:1.55}
	.caddy-timeouts{display:grid;grid-template-columns:repeat(auto-fit,minmax(min(100%,250px),1fr));gap:14px;max-width:760px;margin-top:16px}
	.caddy-timeouts>div>.note{margin:7px 0 0;line-height:1.5}
	.caddy-log-filters{display:grid;grid-template-columns:minmax(180px,1fr) minmax(180px,1fr) auto;gap:10px;margin-bottom:10px}
	.caddy-log-filters .filter-submit{display:flex;align-items:end}
	.table-scroll{overflow:auto}
	.http-code{display:inline-flex;min-width:42px;justify-content:center;padding:4px 8px;border-radius:999px;border:1px solid var(--border);font-size:11px;font-weight:800}
	.http-code.ok{border-color:rgba(56,217,150,.2);background:var(--success-soft);color:#8ceabc}
	.http-code.warn{border-color:rgba(246,196,83,.2);background:var(--warning-soft);color:#f7d884}
	.http-code.danger{border-color:rgba(255,107,122,.25);background:var(--danger-soft);color:#ff9aa5}
	.log-details{position:relative}
	.log-popover{top:100%;right:0}
	.log-error-message{margin:0 0 10px;color:#ff9aa5;font-size:12px;line-height:1.5}
	.resource-value{display:inline-flex;align-items:center;min-width:48px;font-variant-numeric:tabular-nums;color:#d8deea;font-weight:700}
	.compact-action{height:34px;padding:0 10px;border-radius:9px;font-size:12px}
	.app-quick-actions{justify-content:flex-start;flex-wrap:nowrap}
	.app-quick-actions form{display:inline-flex}
	.table-path{display:block;max-width:250px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
	.db-services-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:12px}
	.db-service-card{display:flex;min-height:280px;flex-direction:column}
	.db-service-head{display:flex;align-items:flex-start;justify-content:space-between;gap:14px}
	.db-service-head .eyebrow{margin-bottom:5px;font-size:10px}
	.db-service-facts{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:0 16px;margin-top:18px;border-top:1px solid var(--border);border-bottom:1px solid var(--border)}
	.db-service-facts>div{min-width:0;padding:11px 0;border-bottom:1px solid rgba(255,255,255,.045)}
	.db-service-facts>div:nth-last-child(-n+2){border-bottom:0}
	.db-service-facts .span-2{grid-column:1/-1}
	.db-service-facts span{display:block;margin-bottom:5px;color:var(--muted-2);font-size:9px;font-weight:800;text-transform:uppercase;letter-spacing:.07em}
	.db-service-facts strong{display:block;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;color:#dce2ec;font-size:12px;font-weight:700}

	.db-service-footer{display:flex;min-height:64px;margin-top:auto;padding-top:18px;align-items:flex-end}
	.db-service-footer>form,.db-service-footer>.actions{margin:0;padding:0}
	.db-service-actions{min-height:40px;align-items:center}
	.docker-actions{justify-content:flex-start;flex-wrap:wrap;align-items:center}
	.docker-actions form{display:inline-flex}
	.docker-actions button,.docker-actions summary{white-space:nowrap}
	.docker-metrics{grid-template-columns:repeat(4,minmax(0,1fr))}
	.docker-create-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:14px;align-items:start;margin-top:18px;min-width:0}
	.docker-fold{min-width:0}
	.docker-fold>form{min-width:0}
	.docker-fold>form>.docker-create-grid{min-width:0}
	.docker-create-grid>div{min-width:0}
	.docker-create-grid label{display:block;margin-bottom:6px}
	.docker-create-grid .check-row{display:inline-flex;align-items:center}
	.docker-create-grid .check-row label{margin:0}
	.docker-rebuild-dialog{position:fixed;inset:0;margin:auto;width:min(540px,calc(100vw - 32px));max-height:min(86vh,780px);overflow:auto;padding:24px;background:var(--surface);color:var(--text);border:1px solid var(--border);border-radius:16px;box-shadow:0 20px 80px rgba(0,0,0,.5)}
	.docker-rebuild-dialog::backdrop{background:rgba(0,0,0,.68)}
	.docker-dialog-head{display:flex;align-items:flex-start;justify-content:space-between;gap:12px;margin-bottom:20px}
	.docker-dialog-head h2{font-size:19px;margin:0 0 5px}
	.docker-rebuild-form{display:grid;grid-template-columns:minmax(0,1fr);gap:14px;min-width:0;max-width:none;width:100%;margin:0;padding:0;border:0;background:transparent;box-shadow:none}
	.docker-rebuild-form label{display:block;font-size:12px;white-space:normal}
	.docker-rebuild-form input{display:block;width:100%;min-width:0;margin-top:6px}
	.docker-dialog-grid{display:grid;grid-template-columns:1fr 1fr;gap:12px;min-width:0}
	.docker-dialog-grid label{min-width:0}
	.docker-port{display:flex;flex-direction:column;gap:3px;margin-bottom:5px;white-space:nowrap}
	.docker-port strong{font-size:12px;font-weight:600}
	.docker-port small{font-size:10px;color:var(--muted);line-height:1.35}
	.docker-backups{margin:0 0 16px}
	.docker-backups>summary{padding:14px 18px;cursor:pointer;font-weight:600}
	.docker-backups>summary .note{margin-left:8px;font-weight:400}
	.docker-create-grid>div:not(.docker-runtime-fields):not(.docker-toggle-row){min-width:0}
	.docker-create-grid input,.docker-create-grid textarea{width:100%;min-width:0}
	.docker-key-form{display:grid;grid-template-columns:minmax(0,1fr) auto;align-items:end;gap:12px;max-width:720px}
	.docker-key-form>div{min-width:0}
	.docker-key-form input{width:100%;min-width:0}
	.docker-key-form .docker-create-submit{align-self:end}
	.docker-create-submit{display:flex;align-items:end}
	.docker-add-toolbar{display:flex;align-items:center;gap:10px;flex-wrap:wrap;margin:0 0 16px}
	.docker-fold{scroll-margin-top:18px}
	.docker-fold>summary{display:flex;justify-content:space-between;align-items:center;gap:12px;cursor:pointer;list-style:none}
	.docker-fold>summary::-webkit-details-marker{display:none}
	.docker-fold>summary strong{font-size:16px}
	.docker-fold>summary span{font-size:12px;color:var(--muted-2);text-align:right}
	.docker-fold>summary::before{content:"+";color:var(--primary);font-size:22px;line-height:1}
	.docker-fold[open]>summary::before{content:"−"}
	.docker-toggle-row{grid-column:1/-1;display:flex;align-items:center;gap:22px;flex-wrap:wrap;padding:14px 0;border-top:1px solid var(--border)}
	.docker-toggle-row .check-row{display:inline-flex;align-items:center;margin:0;min-height:40px;white-space:nowrap}
	.docker-shm-field{margin-left:auto;min-width:170px}
	.docker-shm-field input{max-width:170px}
	.docker-row-rebuild{position:relative}
	.docker-row-rebuild summary{display:inline-flex;align-items:center;justify-content:center;cursor:pointer;list-style:none}
	.docker-row-rebuild summary::-webkit-details-marker{display:none}

	.docker-rebuild-form .note{white-space:normal;line-height:1.5}
	.docker-usage{font-variant-numeric:tabular-nums;white-space:nowrap}
	.docker-usage small{display:block;color:var(--muted-2);font-size:11px}
	.docker-runtime-fields{grid-column:1/-1;display:grid;grid-template-columns:minmax(0,1fr) minmax(0,1fr);gap:14px;padding:14px 0 4px;border-top:1px solid var(--border);align-items:start}
	.docker-runtime-fields textarea{width:100%;min-height:118px;font:12px/1.5 ui-monospace,SFMono-Regular,Consolas,monospace}
	.docker-runtime-fields .note{margin:6px 0 0;line-height:1.5}
	.docker-runtime-settings{display:flex;align-items:center;gap:18px;grid-column:1/-1;flex-wrap:wrap}
	.docker-runtime-settings>div{min-width:180px}
	.docker-runtime-settings input[name="shm_size"]{width:160px}

	.empty-state-card{max-width:720px}
	.db-service-card .note{margin:10px 0 0;line-height:1.45}
	.db-service-card>form{margin:0}
	.db-service-actions{justify-content:flex-start;flex-wrap:wrap}
	.backup-settings-grid{display:grid;grid-template-columns:minmax(130px,.7fr) minmax(150px,1fr) minmax(150px,1fr) auto;gap:16px;align-items:end}
	.backup-enabled{height:42px;margin:0}
	.backup-save{display:flex;align-items:end}
	.database-list-head{display:flex;align-items:center;justify-content:space-between;gap:16px;padding-bottom:14px}
	.import-status{display:flex;align-items:flex-start;justify-content:space-between;gap:18px}
	.import-status p{margin:5px 0 0;line-height:1.45}
	.import-status.success{border-color:rgba(56,217,150,.2);background:var(--success-soft);color:#8ceabc}
	.import-status form{flex:0 0 auto}
	.actions>form{display:inline-flex}
	.actions>a,.actions>button,.actions>form>button{flex:0 0 auto}
	.section-title>.secondary,.section-title>.button,.section-title>.danger{flex:0 0 auto}
	.toolbar button,.compact-form button,.app-form-row button{white-space:nowrap}
	.runtime-port-row{display:flex;align-items:center;gap:8px}
	.runtime-port-row .mini-link{color:#9a90ff;font-size:11px;font-weight:800;cursor:pointer}
	.runtime-port-row details{position:relative}
	.deploy-key-block>summary{display:inline-flex}
	.deploy-key-head{display:flex;align-items:center;justify-content:space-between;gap:14px;margin-bottom:10px}
	.deploy-key-value{min-height:86px;resize:none}
	.deploy-auto-switch{margin:0;padding:0;border:0;flex:1}
	.auto-deploy-controls{display:flex;gap:24px;align-items:center;flex-wrap:wrap;margin-top:12px;padding:14px 0;border-top:1px solid var(--border)}
	.auto-deploy-interval{flex:0 0 310px;min-width:225px}
	.auto-deploy-interval label{font-size:12px}
	.auto-deploy-interval input{width:100%;max-width:165px;font-variant-numeric:tabular-nums}
	.auto-deploy-interval .note{margin:6px 0 0;line-height:1.4}

	.terminal-shell{width:min(1220px,calc(100% - 32px))}
	.terminal-panel{overflow:hidden}
	.terminal-toolbar{display:flex;align-items:end;justify-content:space-between;gap:14px;padding:16px;border-bottom:1px solid var(--border);background:rgba(8,23,45,.5)}
	.terminal-toolbar form{display:grid;grid-template-columns:180px minmax(260px,1fr);gap:10px;align-items:end;flex:1}
	.terminal-toolbar label{margin-bottom:5px}
	.terminal-connect{display:flex;align-items:end}
	.terminal-target-meta{display:flex;align-items:center;justify-content:flex-end;gap:10px;min-width:0;padding-bottom:3px}
	.terminal-target-meta code{max-width:420px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
	.terminal-warning{padding:10px 16px;border-bottom:1px solid rgba(246,196,83,.2);background:var(--warning-soft);color:#f7d884;font-size:12px}
	.terminal-screen{height:min(72vh,760px);min-height:460px;padding:10px;background:#071324}
	.terminal-screen .xterm{height:100%}
	.terminal-screen .xterm-viewport{overscroll-behavior:contain}
	.terminal-loading{display:grid;height:100%;place-items:center;color:var(--muted)}
	.app-runtime-card .runtime-facts>div{padding:2px 14px;border:0;border-left:1px solid var(--border);border-radius:0;background:transparent}
	.app-runtime-card .runtime-facts>div:first-child{padding-left:0;border-left:0}
	.app-card .domain-summary>div{padding:3px 0 10px;border:0;border-bottom:1px solid var(--border);border-radius:0;background:transparent}
	.app-card-health .health-grid{gap:0}
	.app-card-health .metric{border:0;border-right:1px solid var(--border);border-radius:0;background:transparent}
	.app-card-health .metric:last-child{border-right:0}
	.statline{display:flex;align-items:center;gap:8px;margin-top:22px;color:#89e8bb;font-size:12px;font-weight:750}
	.statline i{display:block;width:7px;height:7px;border-radius:50%;background:var(--success);box-shadow:0 0 0 5px var(--success-soft)}
	details{position:relative}
	summary{list-style:none;cursor:pointer;border-radius:11px}
	summary::-webkit-details-marker{display:none}
	summary.secondary,summary.danger{width:max-content}
	details[open]>summary.secondary{border-color:rgba(162,201,255,.43);background:var(--surface-2);color:#fff}
	details[open]>summary.danger{border-color:rgba(255,107,122,.5);background:rgba(255,107,122,.18)}
	details>summary:active{transform:none}
	.inline-popover{position:absolute;right:0;z-index:20;width:300px;margin-top:8px;padding:14px;border:1px solid var(--border-strong);border-radius:13px;background:#0e141e;box-shadow:0 22px 65px rgba(0,0,0,.45)}
	.inline-popover.wide{width:460px}
	.inline-popover .button{margin-top:9px}
	.software-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:16px}
	.software-card{display:flex;min-height:255px;flex-direction:column}
	.software-card-head{display:flex;align-items:flex-start;justify-content:space-between;gap:16px}
	.software-card-head .sub{max-width:520px}
	.software-version{margin-top:20px;padding:12px 0;border-top:1px solid var(--border);border-bottom:1px solid var(--border)}
	.software-version span{display:block;margin-bottom:5px;color:var(--muted-2);font-size:10px;font-weight:800;text-transform:uppercase;letter-spacing:.07em}
	.software-version strong{display:block;overflow:hidden;text-overflow:ellipsis;font:12px/1.5 "SFMono-Regular",Consolas,monospace;white-space:nowrap}
	.software-detail{min-height:34px;margin:12px 0 16px;line-height:1.45}
	.software-card form{margin-top:auto}
	.software-task{display:flex;align-items:flex-start;gap:12px;margin-bottom:16px;padding:14px 16px;border:1px solid rgba(246,196,83,.2);border-radius:14px;background:var(--warning-soft)}
	.software-task strong{display:block;font-size:13px}
	.software-task .note{margin:4px 0 0}
	.redis-toolbar{grid-template-columns:150px minmax(260px,1fr) 140px auto}
	.redis-preview{max-height:420px;overflow:auto;white-space:pre-wrap;word-break:break-word}
	.redis-detail code{word-break:break-all}

	@media(max-width:820px){
		.shell{width:min(100% - 20px,1220px)}
		.topbar{height:auto;min-height:60px;padding:10px 12px;flex-wrap:wrap}
		.nav{order:3;width:100%;overflow:visible;justify-content:flex-start;flex-wrap:wrap}
		.toolbar-4,.toolbar-3{grid-template-columns:1fr 1fr}
		main{padding-top:30px}
		.page-head{align-items:flex-start;flex-direction:column}
		.page-control-cluster{justify-content:flex-start;width:100%}
		.service-state{min-height:38px}
		.docker-create-grid{grid-template-columns:1fr 1fr}
		.docker-metrics{grid-template-columns:1fr 1fr}
		.docker-shm-field{margin-left:0}

		h1{font-size:28px}
		table{display:block;overflow-x:auto}
		.inline-popover.wide{width:min(460px,82vw)}
		.metrics-grid{grid-template-columns:1fr 1fr}
		.service-grid-3,.service-grid-4,.security-grid,.domain-summary,.app-overview-grid,.health-grid,.service-summary{grid-template-columns:1fr 1fr}
		.app-form-row-deploy,.app-form-row-db{grid-template-columns:1fr 140px}
		.app-promotion-grid{grid-template-columns:minmax(0,1fr) minmax(0,1fr)}
		.app-promotion-grid>div:last-child{grid-column:1/-1}
		.log-toolbar{grid-template-columns:1fr 1fr}
		.app-dashboard-grid{grid-template-columns:1fr}
		.app-card-wide{grid-column:auto}
		.runtime-head{align-items:flex-start;flex-direction:column}
		.runtime-actions{justify-content:flex-start}
		.caddy-log-filters{grid-template-columns:1fr 1fr}
		.terminal-toolbar{align-items:stretch;flex-direction:column}
		.terminal-toolbar form{width:100%}
		.terminal-target-meta{justify-content:space-between;width:100%;padding-bottom:0}
		.software-grid{grid-template-columns:1fr}
		.redis-toolbar{grid-template-columns:140px minmax(220px,1fr)}
		.backup-settings-grid{grid-template-columns:1fr 1fr}
		.backup-save{align-items:stretch}
		.app-quick-actions{flex-wrap:wrap}
	}
	@media(max-width:520px){
		.metrics-grid,.service-grid-3,.service-grid-4,.security-grid,.compact-form,.compact-form-3,.domain-summary,.app-overview-grid,.health-grid,.app-form-row-deploy,.app-form-row-db,.service-summary,.service-form-grid,.toolbar-4,.toolbar-3{grid-template-columns:1fr}
		.app-promotion-grid{grid-template-columns:1fr}
		.service-form-grid .span-2{grid-column:auto}
		.setting-switch{align-items:flex-start}
		.auto-deploy-controls{align-items:stretch;flex-direction:column;gap:12px}
		.auto-deploy-interval{flex:1;min-width:0}
		.list-toolbar{align-items:stretch;flex-direction:column}
		.list-toolbar input{max-width:none}
		.pager{align-items:flex-start;flex-direction:column}
		.log-toolbar{grid-template-columns:1fr}
		.app-page-head{align-items:flex-start;flex-direction:column}
		.runtime-state>strong{font-size:24px}
		.runtime-facts{grid-template-columns:1fr 1fr}
		.app-runtime-card .runtime-facts>div{padding:8px 0;border-left:0;border-bottom:1px solid var(--border)}
		.app-card-health .metric{border-right:0;border-bottom:1px solid var(--border)}
		.app-card-health .metric:last-child{border-bottom:0}
		.app-card .domain-summary{grid-template-columns:1fr}
		.danger-zone-body{align-items:flex-start;flex-direction:column}
		.caddy-log-filters{grid-template-columns:1fr}
		.terminal-toolbar form{grid-template-columns:1fr}
		.terminal-target-meta{align-items:flex-start;flex-direction:column}
		.page-control-cluster{align-items:stretch;flex-direction:column}
		.service-state{justify-content:space-between;width:100%}
		.db-service-facts{grid-template-columns:1fr}
		.db-service-facts .span-2{grid-column:auto}
		.db-service-facts>div:nth-last-child(-n+2){border-bottom:1px solid rgba(255,255,255,.045)}
		.db-service-facts>div:last-child{border-bottom:0}

		.terminal-screen{min-height:420px;height:68vh}
		.deploy-key-head{align-items:flex-start;flex-direction:column}
		.redis-toolbar{grid-template-columns:1fr}
		.db-services-grid,.backup-settings-grid,.docker-create-grid{grid-template-columns:1fr}
		.docker-metrics{grid-template-columns:1fr 1fr}
		.docker-toggle-row{align-items:flex-start;flex-direction:column;gap:6px}
		.docker-shm-field{width:100%}
		.docker-fold>summary span{display:none}
		.docker-dialog-grid{grid-template-columns:1fr}
		.docker-key-form{grid-template-columns:1fr}
		.docker-rebuild-dialog{padding:18px}
		.docker-runtime-fields{grid-template-columns:1fr}
		.database-list-head,.import-status{align-items:flex-start;flex-direction:column}
		.table-path{max-width:180px}
	}
	/* Shared surfaces: no blur on repeated cards, rows, logs or terminal. */
	:where(.domain-summary>div,.app-overview-grid>div,.service-summary>div,.runtime-facts>div,.attachment-row){background:rgba(8,25,50,.36)}
	:where(.tab,.pager-button,.inline-popover){border-color:var(--border)}
	:where(.inline-popover,.docker-rebuild-dialog){background:linear-gradient(145deg,#203b60,#11233d);box-shadow:0 26px 70px rgba(1,8,25,.5)}
	:where(.badge,.status-badge,.meta-chip){background:rgba(13,32,57,.67);border-color:var(--border);color:#d1e2f7}
	:where(.page-head,.app-page-head,.section-title){min-width:0;flex-wrap:wrap}
	:where(.page-head>div,.app-page-head>div,.section-title>div,.table-scroll,.panel,.app-card,.grid>*,.metrics-grid>*){min-width:0}
	.table-scroll{max-width:100%;overflow-x:auto}
	.shell.terminal-shell{width:min(1480px,calc(100% - 322px))}
	.terminal-toolbar{background:rgba(10,29,54,.64)}
	@media(max-width:1050px){
		.topbar-wrap{position:sticky;inset:auto;top:0;width:100%;height:auto;padding:10px 12px 0;background:linear-gradient(180deg,#081326 50%,rgba(8,19,38,0))}
		.topbar-wrap .shell{width:100%;height:auto;margin:0}
		.topbar{height:auto;min-height:64px;display:flex;flex-direction:row;align-items:center;flex-wrap:wrap;gap:10px;padding:11px 14px;overflow:visible;border-radius:18px}
		.topbar::before{left:20px;right:20px}
		.brand{padding:0}
		.brand-mark{width:36px;height:36px;border-radius:11px}
		.nav{order:3;display:flex;flex-direction:row;flex-wrap:wrap;align-items:center;gap:4px;width:100%;margin:0;padding:8px 0 1px;border-top:1px solid rgba(154,190,239,.13)}
		.nav>a,.nav-dropdown a{min-height:34px;padding:7px 10px;font-size:12px}
		.nav-icon{width:18px;height:18px}
		.nav-icon svg{width:16px;height:16px}
		.nav>details{position:relative;margin:0}
		.nav>details>summary{min-height:34px;padding:7px 9px;font-size:11px;letter-spacing:0;text-transform:none}
		.nav-dropdown{position:absolute;z-index:80;top:calc(100% + 5px);left:0;right:auto;display:flex;min-width:200px;max-width:min(80vw,260px);margin:0;padding:6px;border:1px solid var(--border-strong);border-radius:13px;background:#172f50;box-shadow:0 24px 60px rgba(2,9,25,.45)}
		.nav-dropdown a{white-space:nowrap}
		.sidebar-note{display:none}
		.header-actions{order:2;flex-direction:row;align-items:center;gap:8px;margin:0 0 0 auto;padding:0;border:0}
		.header-actions form,.header-actions button{width:auto}
		.header-actions .secondary{height:34px;padding:0 10px;font-size:11px}
		.shell,.shell.terminal-shell{width:min(100% - 24px,1480px);margin:0 auto}
		main{padding:30px 0 65px}
	}
	@media(max-width:620px){
		.topbar-wrap{padding:8px 8px 0}
		.topbar{gap:8px;padding:10px}
		.brand-copy strong{font-size:12px}
		.brand-copy small{font-size:8px}
		.brand-mark{width:32px;height:32px}
		.nav{gap:2px;padding-top:6px}
		.nav>a,.nav>details>summary{min-height:32px;padding:6px 8px}
		.nav a{gap:6px;font-size:11px}
		.header-actions{gap:5px}
		.header-actions .secondary{height:31px;padding:0 8px;font-size:10px}
		.shell,.shell.terminal-shell{width:calc(100% - 16px)}
		main{padding-top:24px}
		.panel-pad{padding:16px}
		.card{padding:17px}
		.metric{padding:14px}
		th,td{padding:11px 12px}
	}
	@media(prefers-reduced-motion:reduce){
		:where(button,.button,.secondary,.danger,.icon-button,.pager-button,.nav a,.card,.tab,.switch i,.switch i::after){transition:none!important;animation:none!important}
	}

`

const tablePagerScript = `<script>
document.addEventListener('DOMContentLoaded', () => {
  document.querySelectorAll('table').forEach((table) => {
    if (table.dataset.noPager === '1') return;
    const tbody = table.tBodies[0];
    if (!tbody) return;
    const allRows = Array.from(tbody.rows);
    if (allRows.length <= 25) return;

    let page = 1;
    let perPage = 25;
    let query = '';

    const tools = document.createElement('div');
    tools.className = 'table-tools';
    tools.innerHTML = '<input class="table-tools-search" type="search" placeholder="Search this table"><div class="table-tools-right"><span class="pager-info"></span><select aria-label="Rows per page"><option>25</option><option>50</option><option>100</option></select><button type="button" class="secondary" data-prev>Previous</button><button type="button" class="secondary" data-next>Next</button></div>';
    table.parentNode.insertBefore(tools, table);

    const search = tools.querySelector('input');
    const select = tools.querySelector('select');
    const info = tools.querySelector('.pager-info');
    const prev = tools.querySelector('[data-prev]');
    const next = tools.querySelector('[data-next]');

    const render = () => {
      const filtered = allRows.filter((row) => row.textContent.toLowerCase().includes(query));
      const pages = Math.max(1, Math.ceil(filtered.length / perPage));
      page = Math.min(Math.max(1, page), pages);
      const start = (page - 1) * perPage;
      const visible = new Set(filtered.slice(start, start + perPage));
      allRows.forEach((row) => { row.hidden = !visible.has(row); });
      info.textContent = filtered.length + ' items · page ' + page + ' of ' + pages;
      prev.disabled = page <= 1;
      next.disabled = page >= pages;
    };

    search.addEventListener('input', () => {
      query = search.value.trim().toLowerCase();
      page = 1;
      render();
    });
    select.addEventListener('change', () => {
      perPage = Number(select.value) || 25;
      page = 1;
      render();
    });
    prev.addEventListener('click', () => { if (page > 1) page--; render(); });
    next.addEventListener('click', () => { page++; render(); });
    render();
  });
});
</script>`

func pageHead(title string) string {
	return `<!doctype html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>` + html.EscapeString(title) + ` · Open Go Panel</title>
	<style>` + baseStyles + `</style>` + tablePagerScript + `
</head>`
}

func appHeader(active string) string {
	type menuItem struct {key,href,label string}
	link := func(item menuItem) string {
		class := ""
		if item.key == active {class = ` class="active" aria-current="page"`}
		return `<a` + class + ` href="` + item.href + `">` + item.label + `</a>`
	}
	group := func(title string, children ...menuItem) string {
		current := false
		var contents strings.Builder
		for _, item := range children {
			if item.key == active {current = true}
			contents.WriteString(link(item))
		}
		openClass := ""
		if current {openClass = ` class="current"`}
		return `<details` + openClass + `><summary>` + title + `</summary><div class="nav-dropdown">` +
			contents.String() + `</div></details>`
	}
	var nav strings.Builder
	for _, item := range []menuItem{
		{"overview", "/", "Overview"},
		{"apps", "/apps", "Apps"},
		{"databases", "/databases", "Databases"},
		{"docker", "/docker", "Docker"},
	} {nav.WriteString(link(item))}
	nav.WriteString(group("Infrastructure",
		menuItem{"caddy","/caddy","Caddy & domains"},
		menuItem{"users","/users","Users"},
		menuItem{"software","/software","Software"},
		menuItem{"terminal","/terminal","Terminal"},
	))
	nav.WriteString(group("Observability",
		menuItem{"performance","/performance","Performance"},
		menuItem{"log-retention","/log-retention","Logs & retention"},
		menuItem{"activity","/activity","Activity"},
	))
	nav.WriteString(link(menuItem{"security","/security","Security"}))
	return `<div class="topbar-wrap"><div class="shell"><header class="topbar">
		<a class="brand" href="/"><span class="brand-mark">OG</span><span>Open Go Panel</span></a>
		<nav class="nav" aria-label="Main navigation">` + nav.String() + `</nav>
		<div class="header-actions">
			<form method="post" action="/panel/close" onsubmit="return confirm('Stop and disable the panel service? To reopen it, use SSH: sudo systemctl start open-go-panel.service')"><button class="secondary compact-action" type="submit" title="Stop the panel service and close its port">Close panel</button></form>
			<form method="post" action="/logout"><button class="secondary" type="submit">Logout</button></form>
		</div>
	</header></div></div>`
}
