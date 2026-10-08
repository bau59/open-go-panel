package server

import (
	"html"
	"strings"
)

const baseStyles = `
	:root{
		--bg:#0a0d14;
		--surface:#10151f;
		--surface-2:#151b27;
		--surface-3:#1b2230;
		--border:#252d3d;
		--border-strong:#333d50;
		--text:#f3f6fb;
		--muted:#909aab;
		--muted-2:#667085;
		--primary:#6d5dfc;
		--primary-hover:#7c6cff;
		--primary-soft:rgba(109,93,252,.14);
		--success:#38d996;
		--success-soft:rgba(56,217,150,.12);
		--warning:#f6c453;
		--warning-soft:rgba(246,196,83,.12);
		--danger:#ff6b7a;
		--danger-soft:rgba(255,107,122,.12);
		--radius:14px;
		--radius-lg:20px;
		--shadow:0 18px 50px rgba(0,0,0,.22);
	}
	*{box-sizing:border-box}
	html{background:var(--bg)}
	body{margin:0;min-height:100vh;background:
		radial-gradient(circle at 20% 0%,rgba(109,93,252,.09),transparent 28rem),
		var(--bg);color:var(--text);font-family:Inter,ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}
	a{color:inherit;text-decoration:none}
	button,input,select,textarea{font:inherit}
	button{cursor:pointer}
	.shell{width:min(1220px,calc(100% - 32px));margin:0 auto}
	.topbar-wrap{position:sticky;top:0;z-index:40;padding:14px 0 0;background:linear-gradient(var(--bg) 65%,transparent)}
	.topbar{height:62px;display:flex;align-items:center;justify-content:space-between;gap:18px;padding:0 14px 0 16px;border:1px solid var(--border);border-radius:18px;background:rgba(16,21,31,.92);box-shadow:var(--shadow);backdrop-filter:blur(18px)}
	.brand{display:flex;align-items:center;gap:11px;font-size:14px;font-weight:750;letter-spacing:-.01em}
	.brand-mark{display:grid;place-items:center;width:32px;height:32px;border-radius:10px;background:linear-gradient(145deg,#7c6cff,#5146d8);box-shadow:0 8px 22px rgba(109,93,252,.28);font-size:11px;font-weight:850;color:#fff}
	.nav{display:flex;align-items:center;gap:4px;padding:4px;border:1px solid var(--border);border-radius:12px;background:#0c111a}
	.nav a{padding:8px 12px;border-radius:9px;color:var(--muted);font-size:13px;font-weight:650;transition:.15s ease}
	.nav a:hover{color:var(--text);background:var(--surface-2)}
	.nav a.active{color:#fff;background:var(--primary-soft);box-shadow:inset 0 0 0 1px rgba(109,93,252,.25)}
	.header-actions{display:flex;align-items:center;gap:10px}
	.icon-button,.button,.secondary,.danger{display:inline-flex;align-items:center;justify-content:center;height:40px;padding:0 14px;border-radius:11px;border:1px solid transparent;font-weight:700;font-size:13px;transition:.15s ease}
	.button{background:var(--primary);color:#fff;box-shadow:0 8px 22px rgba(109,93,252,.18)}
	.button:hover{background:var(--primary-hover)}
	.secondary,.icon-button{border-color:var(--border-strong);background:var(--surface);color:#c8d0dc}
	.secondary:hover,.icon-button:hover{background:var(--surface-2);color:#fff}
	.danger{border-color:rgba(255,107,122,.3);background:var(--danger-soft);color:#ff9aa5}
	main{padding:42px 0 72px}
	.page-head{display:flex;align-items:flex-end;justify-content:space-between;gap:20px;margin-bottom:24px}
	.eyebrow{margin:0 0 8px;color:#9a90ff;font-size:12px;font-weight:800;letter-spacing:.08em;text-transform:uppercase}
	h1{margin:0;font-size:32px;line-height:1.15;letter-spacing:-.035em}
	h2{margin:0;font-size:16px;letter-spacing:-.015em}
	.sub{margin:9px 0 0;color:var(--muted);font-size:14px;line-height:1.55}
	.panel{border:1px solid var(--border);border-radius:var(--radius-lg);background:linear-gradient(180deg,rgba(21,27,39,.92),rgba(16,21,31,.96));box-shadow:0 12px 36px rgba(0,0,0,.12)}
	.panel-pad{padding:22px}
	.grid{display:grid;gap:16px}
	.cards{grid-template-columns:repeat(auto-fit,minmax(230px,1fr))}
	.card{position:relative;min-height:156px;padding:22px;border:1px solid var(--border);border-radius:var(--radius-lg);background:linear-gradient(180deg,var(--surface-2),var(--surface));transition:.16s ease}
	a.card:hover{transform:translateY(-2px);border-color:#3a455a;background:linear-gradient(180deg,#192131,#111823)}
	.card-icon{display:grid;place-items:center;width:38px;height:38px;margin-bottom:24px;border:1px solid rgba(109,93,252,.22);border-radius:12px;background:var(--primary-soft);color:#a89fff;font-size:12px;font-weight:850}
	.card h2{margin-bottom:7px}
	.card p{margin:0;color:var(--muted);font-size:13px;line-height:1.55}
	.toolbar{display:grid;gap:10px;padding:16px;border-bottom:1px solid var(--border)}
	.field,input,select,textarea{width:100%;border:1px solid var(--border-strong);border-radius:11px;background:#0d121b;color:var(--text);outline:none;transition:.15s ease}
	input,select{height:42px;padding:0 12px}
	select{
		appearance:none;
		-webkit-appearance:none;
		padding-right:42px;
		background-color:#0d121b;
		background-image:url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='14' height='14' viewBox='0 0 24 24' fill='none' stroke='%23909aab' stroke-width='2' stroke-linecap='round' stroke-linejoin='round'%3E%3Cpath d='m6 9 6 6 6-6'/%3E%3C/svg%3E");
		background-repeat:no-repeat;
		background-position:right 14px center;
	}
	select:hover{border-color:#465268}
	select option{background:#10151f;color:var(--text)}
	.check-row{display:flex;align-items:center;gap:9px;color:#c8d0dc;font-size:13px;font-weight:650;cursor:pointer}
	.check-row input[type="checkbox"]{width:17px;height:17px;margin:0;accent-color:var(--primary)}
	.service-grid-3{grid-template-columns:repeat(3,minmax(0,1fr))}
	.service-grid-4{grid-template-columns:repeat(4,minmax(0,1fr))}
	textarea{padding:11px 12px;resize:vertical}
	input:focus,select:focus,textarea:focus{border-color:#675af0;box-shadow:0 0 0 3px rgba(109,93,252,.11)}
	input::placeholder,textarea::placeholder{color:#566074}
	label{display:block;margin-bottom:7px;color:#b8c0cd;font-size:12px;font-weight:700}
	table{width:100%;border-collapse:collapse}
	th{padding:12px 16px;border-bottom:1px solid var(--border);color:var(--muted-2);font-size:11px;text-align:left;text-transform:uppercase;letter-spacing:.07em;font-weight:800}
	td{padding:15px 16px;border-bottom:1px solid #1d2431;font-size:13px;vertical-align:middle}
	tbody tr:last-child td{border-bottom:0}
	tbody tr:hover{background:rgba(255,255,255,.012)}
	.muted,.note{color:var(--muted-2);font-size:12px}
	.muted{margin-top:4px}
	.badge,.status-badge{display:inline-flex;align-items:center;gap:7px;padding:5px 9px;border-radius:999px;border:1px solid var(--border);background:#111824;color:#bac3d1;font-size:11px;font-weight:750}
	.status-badge::before{content:"";width:6px;height:6px;border-radius:50%;background:#778196}
	.status-badge.ok{border-color:rgba(56,217,150,.2);background:var(--success-soft);color:#8ceabc}
	.status-badge.ok::before{background:var(--success)}
	.status-badge.warn{border-color:rgba(246,196,83,.2);background:var(--warning-soft);color:#f7d884}
	.status-badge.warn::before{background:var(--warning)}
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
	.section-title{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:18px}
	.metrics-grid{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:12px}
	.metric{padding:18px;border:1px solid var(--border);border-radius:16px;background:linear-gradient(180deg,var(--surface-2),var(--surface))}
	.metric span{display:block;color:var(--muted-2);font-size:11px;font-weight:800;text-transform:uppercase;letter-spacing:.07em}
	.metric strong{display:block;margin-top:8px;font-size:18px;letter-spacing:-.02em}
	.metric small{display:block;margin-top:7px;color:var(--muted);font-size:11px}
	.meter{height:6px;margin-top:11px;overflow:hidden;border-radius:999px;background:#0b1018}
	.meter i{display:block;height:100%;border-radius:inherit;background:linear-gradient(90deg,var(--primary),#8d82ff)}
	.tabs{display:flex;gap:6px;margin-bottom:18px}
	.tab{padding:7px 10px;border:1px solid var(--border);border-radius:9px;background:#0d131d;color:var(--muted);font-size:12px;font-weight:700}
	.codearea{min-height:320px;font-family:"SFMono-Regular",Consolas,monospace;font-size:12px;line-height:1.55}
	.logbox{min-height:420px;max-height:65vh;overflow:auto;margin:0;padding:18px;border:1px solid var(--border);border-radius:14px;background:#080c12;color:#c6cfdd;font:12px/1.6 "SFMono-Regular",Consolas,monospace;white-space:pre-wrap}
	.security-output{max-height:420px;overflow:auto;margin:0;padding:16px;border:1px solid var(--border);border-radius:12px;background:#080c12;color:#c6cfdd;font:11px/1.55 "SFMono-Regular",Consolas,monospace;white-space:pre;overscroll-behavior:contain}
	.compact-form{display:grid;grid-template-columns:1fr auto;gap:8px;margin-top:12px}
	.compact-form-3{grid-template-columns:1fr 1fr auto}
	.security-grid{grid-template-columns:1fr 1fr}
	.settings-list{display:grid;gap:10px}
	.setting-switch{display:flex;align-items:center;justify-content:space-between;gap:20px;margin:0;padding:14px 0;border-bottom:1px solid var(--border);cursor:pointer}
	.setting-switch:last-child{border-bottom:0}
	.setting-switch strong{display:block;color:var(--text);font-size:14px}
	.setting-switch>div>span{display:block;margin-top:4px;color:var(--muted);font-size:12px;line-height:1.45}
	.switch{position:relative;display:inline-flex!important;width:44px;height:24px;flex:0 0 auto}
	.switch input{position:absolute;opacity:0;pointer-events:none}
	.switch i{position:absolute;inset:0;border:1px solid var(--border-strong);border-radius:999px;background:#0b1018;transition:.18s ease}
	.switch i::after{content:"";position:absolute;top:3px;left:3px;width:16px;height:16px;border-radius:50%;background:#7d8798;transition:.18s ease}
	.switch input:checked+i{border-color:rgba(109,93,252,.5);background:var(--primary)}
	.switch input:checked+i::after{transform:translateX(20px);background:#fff}
	.domain-summary{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:10px}
	.domain-summary>div,.app-overview-grid>div{min-width:0;padding:13px 14px;border:1px solid var(--border);border-radius:13px;background:#0d121b}
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
	.app-form-row{display:grid;gap:8px}
	.app-form-row-deploy{grid-template-columns:minmax(0,1fr) 160px auto}
	.app-form-row-db{grid-template-columns:minmax(0,1fr) 170px auto}
	.service-summary{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:10px}
	.service-summary>div{min-width:0;padding:13px 14px;border:1px solid var(--border);border-radius:13px;background:#0d121b}
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
	.pager-button{display:inline-flex;align-items:center;justify-content:center;height:36px;padding:0 12px;border:1px solid var(--border-strong);border-radius:10px;background:var(--surface);color:#c8d0dc;font-size:12px;font-weight:700}
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
	.app-title-row{display:flex;align-items:center;gap:10px}
	.app-runtime-card{margin-bottom:16px}
	.runtime-head{display:flex;align-items:center;justify-content:space-between;gap:20px}
	.runtime-state{display:flex;align-items:center;gap:10px}
	.runtime-state>strong{font-size:28px;letter-spacing:-.03em}
	.runtime-actions{justify-content:flex-end}
	.runtime-facts{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:10px;margin-top:18px}
	.runtime-facts>div{min-width:0;padding:12px 13px;border:1px solid var(--border);border-radius:12px;background:#0d121b}
	.runtime-facts span{display:block;margin-bottom:6px;color:var(--muted-2);font-size:10px;font-weight:800;text-transform:uppercase;letter-spacing:.07em}
	.runtime-facts strong,.runtime-facts code{display:block;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-size:12px}
	.app-dashboard-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:16px}
	.app-card{min-width:0}
	.app-card-wide{grid-column:1/-1}
	.app-card .domain-summary{grid-template-columns:repeat(2,minmax(0,1fr))}
	.attachment-row{padding:12px 13px;margin:8px 0;border:1px solid var(--border);border-radius:12px;background:#0d121b}
	.service-settings-card{scroll-margin-top:92px}
	.service-settings-body{margin-top:18px;padding-top:18px;border-top:1px solid var(--border)}
	.danger-zone{border-color:rgba(255,107,122,.18)}
	.danger-zone-body{display:flex;align-items:center;justify-content:space-between;gap:20px;margin-top:18px;padding-top:18px;border-top:1px solid rgba(255,107,122,.18)}
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
	.runtime-port-row{display:flex;align-items:center;gap:8px}
	.runtime-port-row .mini-link{color:#9a90ff;font-size:11px;font-weight:800;cursor:pointer}
	.runtime-port-row details{position:relative}
	.deploy-key-block>summary{display:inline-flex}
	.deploy-key-head{display:flex;align-items:center;justify-content:space-between;gap:14px;margin-bottom:10px}
	.deploy-key-value{min-height:86px;resize:none}
	.terminal-shell{width:min(1440px,calc(100% - 32px))}
	.terminal-panel{overflow:hidden}
	.terminal-toolbar{display:flex;align-items:end;justify-content:space-between;gap:16px;padding:16px;border-bottom:1px solid var(--border);background:#0d121b}
	.terminal-toolbar form{display:grid;grid-template-columns:180px minmax(260px,1fr) auto;gap:10px;align-items:end;flex:1}
	.terminal-toolbar label{margin-bottom:5px}
	.terminal-connect{display:flex;align-items:end}
	.terminal-target-meta{display:flex;align-items:center;gap:10px;min-width:0}
	.terminal-target-meta code{max-width:420px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
	.terminal-warning{padding:10px 16px;border-bottom:1px solid rgba(246,196,83,.2);background:var(--warning-soft);color:#f7d884;font-size:12px}
	.terminal-screen{height:min(72vh,760px);min-height:460px;padding:10px;background:#080c12}
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
	summary{list-style:none}
	summary::-webkit-details-marker{display:none}
	.inline-popover{position:absolute;right:0;z-index:20;width:300px;margin-top:8px;padding:14px;border:1px solid var(--border-strong);border-radius:13px;background:#0e141e;box-shadow:0 22px 65px rgba(0,0,0,.45)}
	.inline-popover.wide{width:460px}
	.inline-popover .button{margin-top:9px}
	@media(max-width:820px){
		.shell{width:min(100% - 20px,1220px)}
		.topbar{height:auto;min-height:60px;padding:10px 12px;flex-wrap:wrap}
		.nav{order:3;width:100%;overflow:auto;justify-content:flex-start}
		main{padding-top:30px}
		.page-head{align-items:flex-start;flex-direction:column}
		h1{font-size:28px}
		table{display:block;overflow-x:auto}
		.inline-popover.wide{width:min(460px,82vw)}
		.metrics-grid{grid-template-columns:1fr 1fr}
		.service-grid-3,.service-grid-4,.security-grid,.domain-summary,.app-overview-grid,.health-grid,.service-summary{grid-template-columns:1fr 1fr}
		.app-form-row-deploy,.app-form-row-db{grid-template-columns:1fr 140px}
		.log-toolbar{grid-template-columns:1fr 1fr}
		.app-dashboard-grid{grid-template-columns:1fr}
		.app-card-wide{grid-column:auto}
		.runtime-head{align-items:flex-start;flex-direction:column}
		.runtime-actions{justify-content:flex-start}
		.caddy-log-filters{grid-template-columns:1fr 1fr}
		.terminal-toolbar{align-items:stretch;flex-direction:column}
		.terminal-toolbar form{width:100%}
		.terminal-target-meta{justify-content:space-between;width:100%}
	}
	@media(max-width:520px){
		.metrics-grid,.service-grid-3,.service-grid-4,.security-grid,.compact-form,.compact-form-3,.domain-summary,.app-overview-grid,.health-grid,.app-form-row-deploy,.app-form-row-db,.service-summary,.service-form-grid{grid-template-columns:1fr}
		.service-form-grid .span-2{grid-column:auto}
		.setting-switch{align-items:flex-start}
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
		.terminal-screen{min-height:420px;height:68vh}
		.deploy-key-head{align-items:flex-start;flex-direction:column}
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
	nav := []struct {
		key   string
		href  string
		label string
	}{
		{"overview", "/", "Overview"},
		{"apps", "/apps", "Apps"},
		{"users", "/users", "Users"},
		{"databases", "/databases", "Databases"},
		{"caddy", "/caddy", "Caddy"},
		{"security", "/security", "Security"},
		{"terminal", "/terminal", "Terminal"},
		{"activity", "/activity", "Activity"},
	}

	var items strings.Builder
	for _, item := range nav {
		class := ""
		if item.key == active {
			class = ` class="active"`
		}
		items.WriteString(`<a` + class + ` href="` + item.href + `">` + item.label + `</a>`)
	}

	return `<div class="topbar-wrap"><div class="shell"><header class="topbar">
		<a class="brand" href="/"><span class="brand-mark">OG</span><span>Open Go Panel</span></a>
		<nav class="nav">` + items.String() + `</nav>
		<div class="header-actions">
			<form method="post" action="/logout"><button class="secondary" type="submit">Logout</button></form>
		</div>
	</header></div></div>`
}
