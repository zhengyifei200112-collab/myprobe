import * as echarts from 'echarts/core'
import { LineChart } from 'echarts/charts'
import { DataZoomComponent, GridComponent, LegendComponent, TooltipComponent, MarkLineComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'

echarts.use([LineChart, DataZoomComponent, GridComponent, LegendComponent, TooltipComponent, MarkLineComponent, CanvasRenderer])

export default echarts
