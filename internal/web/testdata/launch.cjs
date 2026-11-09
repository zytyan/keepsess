const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
exports.launch=(options={})=>chromium.launch({
 ...options,
 ...(process.env.KEEPSESS_BROWSER_CHANNEL?{channel:process.env.KEEPSESS_BROWSER_CHANNEL}:{}),
 ...(process.env.KEEPSESS_BROWSER_HEADED==='1'?{headless:false}:{}),
});
